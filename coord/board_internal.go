// board_internal.go —— Board 适配器的内部件（键集构造 / 断言面 / 解码 / 凭据铸造）。
// 从 board.go 拆出：结构阈值（400 行）按判据的一侧切——适配器的判定入口
// 与它们消费的内部件分开，两边各自可读。
package coord

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
	"github.com/yi-nology/goagent-kernel/tenant"
)

// ── 内部件 ────────────────────────────────────────────────────────────────

// TenantScoped 报告适配器是否为租户作用域（装配期决策的投影）。
//
// 宿主可在启动自检里断言「多租户部署拿到的是租户作用域适配器」——
// 单租户适配器与租户适配器的构造器不同，这条断言把「忘记装配」变成
// 启动期失败，而不是运行期的跨租户可见。
func (a *BoardAdapter) TenantScoped() bool { return a.tenantScoped }

// scope 取本次操作的作用域：单租户恒为空；租户作用域从 ctx 取，缺失即拒。
//
// 这是**所有入口的第一步**：缺租户的拒绝先于任何存储读写（拒绝不得有
// 副作用，也不得先按空作用域读一遍再拒绝）。
func (a *BoardAdapter) scope(ctx context.Context) (string, error) {
	if !a.tenantScoped {
		return "", nil
	}
	c, err := tenant.Require(ctx)
	if err != nil {
		return "", err
	}
	return c.Tenant, nil
}

// taskPrefix 任务键前缀（按租户作用域生成；空租户 = 旧布局）。
//
// 单租户：coord/task/<id>（逐字不变）。
// 租户作用域：coord/t/<escaped-tenant>/task/<id>——**独立根** coord/t/，
// 不与旧布局的 coord/task/ 重叠：旧布局的任务 id 是任意字符串（可含 '/'），
// 若租户段直接接在 coord/task/ 后面，就会与「旧布局里 id 恰好是
// <tenant>/<id> 的任务」撞键。转义只做在租户段上：id 是该租户根下的
// 不透明后缀，同租户内不同 id 必然不同键。
func taskPrefix(tenantID string) string {
	if tenantID == "" {
		return taskKeyPrefix
	}
	return tenantRootPrefix + escapeTenant(tenantID) + "/task/"
}

// taskKey 一个任务记录的键（键只由作用域生成，调用方不拼租户）。
func taskKey(tenantID string, id taskboard.TaskID) string {
	if tenantID == "" {
		return taskKeyPrefix + string(id)
	}
	return tenantRootPrefix + escapeTenant(tenantID) + "/task/" + string(id)
}

// fenceKey 代次计数器的键（与任务键同一作用域）。
func fenceKey(tenantID string, id taskboard.TaskID) string {
	if tenantID == "" {
		return fenceKeyPrefix + string(id)
	}
	return tenantRootPrefix + escapeTenant(tenantID) + "/taskfence/" + string(id)
}

// escapeTenant 把租户名编码成单一路径段：只保留 [A-Za-z0-9_-]，其余
// 字节按 %XX 转义（含 '%' 本身）。编码是单射——不同租户名不可能拼出
// 同一个键前缀，租户段也无法伪造额外的路径分段。
func escapeTenant(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0x0f])
		}
	}
	return b.String()
}

// claimKeySet 认领判定的完整键集：任务自身 + 代次计数器 + 全部依赖键（去重，
// 同租户作用域）。
func claimKeySet(tenantID string, id taskboard.TaskID, deps []taskboard.TaskID) []string {
	tk := taskKey(tenantID, id)
	keys := []string{tk, fenceKey(tenantID, id)}
	seen := map[string]bool{tk: true}
	for _, d := range deps {
		k := taskKey(tenantID, d)
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	return keys
}

// decodeFence 解码代次计数器（键不存在从 0 起；内容损坏报 ErrMalformedRecord
// ——按 0 起会破坏「严格递增」，静默吞掉比报错贵）。
func decodeFence(e Entry) (uint64, error) {
	if !e.Exists {
		return 0, nil
	}
	var v uint64
	if err := json.Unmarshal(e.Val, &v); err != nil {
		return 0, errors.Wrap(ErrMalformedRecord, "代次计数器解码失败", "key", e.Key)
	}
	return v, nil
}

// sameStrings 报告两个键集（不计顺序）是否相同。
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ma := make(map[string]int, len(a))
	for _, s := range a {
		ma[s]++
	}
	for _, s := range b {
		ma[s]--
		if ma[s] < 0 {
			return false
		}
	}
	return true
}

// assertsFrom 把快照里读过的全部键原样列进断言（少列一个就是少护一扇窗）。
func assertsFrom(snap map[string]Entry) []Assert {
	out := make([]Assert, 0, len(snap))
	for k, e := range snap {
		out = append(out, Assert{Key: k, Ver: e.Ver})
	}
	return out
}

// requireClaimed 取「已认领且令牌匹配」的任务；不匹配报 ErrLeaseLost。
//
// scope 是本次操作的作用域：记录里的租户必须与它一致（不一致按
// ErrMalformedRecord 拒，不把别人的记录当自己的状态判）。
func requireClaimed(e Entry, scope string, id taskboard.TaskID, token string) (taskboard.Task, error) {
	if !e.Exists {
		return taskboard.Task{}, errors.Wrap(taskboard.ErrNotFound, "任务不存在", "task", string(id))
	}
	cur, err := decodeTaskIn(e, scope)
	if err != nil {
		return taskboard.Task{}, err
	}
	if cur.State != taskboard.StateClaimed || cur.Lease.Token != token {
		return taskboard.Task{}, errors.Wrap(taskboard.ErrLeaseLost,
			"租约令牌不匹配（租约已易主或被释放过）", "task", string(id))
	}
	return cur, nil
}

// mintToken 铸一枚认领令牌（crypto/rand，128 位，授予之间不重合）。
func mintToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", errors.Wrap(err, "coord: 铸造认领令牌失败")
	}
	return hex.EncodeToString(b[:]), nil
}

// checkSeedScope Seed 的作用域装配检查：摆入的任务必须与适配器的作用域
// 形态一致。跨作用域静默落键会让任务「写进去了但自己的 List 看不见」——
// 装载期就响，不留给运行期猜。
func (a *BoardAdapter) checkSeedScope(tenantID string) error {
	switch {
	case a.tenantScoped && tenantID == "":
		return errors.New("coord: 租户作用域适配器不能摆入空租户任务（请给 Task.Tenant）")
	case !a.tenantScoped && tenantID != "":
		return errors.New("coord: 单租户适配器不能摆入带租户任务（请用 NewTenantBoardAdapter）")
	}
	return nil
}

// decodeTask 解码任务记录。
func decodeTask(e Entry) (taskboard.Task, error) {
	var t taskboard.Task
	if !e.Exists {
		return t, nil
	}
	if err := json.Unmarshal(e.Val, &t); err != nil {
		return taskboard.Task{}, errors.Wrap(ErrMalformedRecord,
			"任务记录解码失败（键下内容不是本适配器的编码）", "key", e.Key)
	}
	return t, nil
}

// decodeTaskIn 解码并核对租户作用域：记录的 Tenant 必须与键的租户段一致。
//
// 不一致说明键布局与记录内容分叉（旧数据被放错命名空间，或有人绕过适配器
// 写键）——放行会把 A 租户的记录当成 B 租户的状态判，静默给出错误结论。
func decodeTaskIn(e Entry, scope string) (taskboard.Task, error) {
	t, err := decodeTask(e)
	if err != nil || !e.Exists {
		return t, err
	}
	if t.Tenant != scope {
		return taskboard.Task{}, errors.Wrap(ErrMalformedRecord,
			"任务记录的租户与键的租户段不一致（拒绝跨租户读取）",
			"key", e.Key, "scope", scope, "record_tenant", t.Tenant)
	}
	return t, nil
}

// setTaskMutation 编码整份任务为一次写入（键由作用域生成）。
func setTaskMutation(scope string, t taskboard.Task) ([]Mutation, error) {
	t.Tenant = scope
	enc, err := json.Marshal(t)
	if err != nil {
		return nil, errors.Wrap(err, "coord: 任务编码失败", "task", string(t.ID))
	}
	return []Mutation{{Key: taskKey(scope, t.ID), Val: enc}}, nil
}
