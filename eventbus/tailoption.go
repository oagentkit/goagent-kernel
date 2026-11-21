// tailoption.go —— Tailer 的装配选项（With*）。
//
// 从 tail.go 按关注点拆出（issue #448）：装配面与运行时分开。
// 公开契约面逐字未变（只是搬家，零导出符号增删、零签名变更）。
package eventbus

import "time"

// TailerOption 尾随器可选项。
type TailerOption func(*Tailer)

// WithTailInterval 覆盖尾随间隔（d <= 0 归缺省 DefaultTailInterval）。
func WithTailInterval(d time.Duration) TailerOption {
	return func(t *Tailer) {
		if d > 0 {
			t.interval = d
		}
	}
}

// WithTailBatchLimit 覆盖单拍批量上限（n <= 0 归缺省 DefaultTailLimit）。
func WithTailBatchLimit(n int) TailerOption {
	return func(t *Tailer) {
		if n > 0 {
			t.limit = n
		}
	}
}

// WithTailStartCursor 显式起始游标：**跳过 MaxID 初始化**，直接从给定值起随。
//
// 缺省（不给本选项）走 MaxID 初始化——从当前尾部起随、不回放存量，那是
// 补灌面的常态。显式给游标用于宿主已知起点的形态（如从上次停机点续随）；
// 给 0 即从头回放，回放量由宿主自己负责。
//
// 同时装配了 CursorStore 时，**已持久化的游标优先**：LoadCursor 命中
// （found=true）就从持久化值起随；未命中（found=false，首次部署）才回退
// 到本选项的显式起点，再回退到 MaxID。
func WithTailStartCursor(id uint64) TailerOption {
	return func(t *Tailer) {
		t.startCursor = id
		t.useMaxID = false
	}
}

// WithTailCursorStore 装配游标持久化接缝（issue #183）。
//
// 装配后初始化顺序：LoadCursor 命中 ⇒ 从持久化游标起随（重启续随，停机
// 窗口内的事件仍被补灌）；未命中 ⇒ 回退显式起点 / MaxID（首次部署）；
// Load 失败 ⇒ 停摆重试，**绝不静默回退 MaxID**（那会跳过停机窗口）。
// 游标每前进一拍即 SaveCursor；保存失败只 warn 不停摆（游标落后只会
// 重读，订阅侧幂等吸收）。
func WithTailCursorStore(s CursorStore) TailerOption {
	return func(t *Tailer) {
		if s != nil {
			t.cursorStore = s
		}
	}
}

// WithTailOnGap 注入游标空洞哨兵（issue #183）：Tailer 观测到跳号——
// 返回段的首条 ID 大于游标+1，或段内相邻两条不连续——即回调
// fn(from, to)（from..to = 被跳过的 ID 闭区间）。缺省走包内 warn 日志。
//
// 哨兵响 = TailStore 违反「无空洞推进」契约（乱序提交漏防或行被删），
// 被跳的 ID 大概率**永久丢失**（游标已越过）——宿主该把它接告警/指标，
// 不是当正常路径。回调同步调用、在尾随拍内：不得阻塞、不得回调 Publish。
func WithTailOnGap(fn func(from, to uint64)) TailerOption {
	return func(t *Tailer) {
		if fn != nil {
			t.onGap = fn
		}
	}
}
