// key_scope.go —— 动作身份的**作用域段拼装机制**（issue #261）。
//
// # 它补的是哪一段
//
// sideeffect 的幂等键判据是「同一 Key 只施加一次」，但键的**拼法**此前
// 完全归宿主：受管执行器的默认键只用任务 id（"task:" + id），多租户/
// 多运行的宿主共享一份副作用存储时，同名任务派生出同一个键——一个租户
// 的动作被另一个租户「白嫖」成已完成（静默，结果看起来正常），或撞上
// ErrActionConflict。宿主想规避只能自己每次显式拼 Key——拼法分散就是
// 口径漂移的开始。
//
// # 词汇归宿主，机制归本仓
//
// 租户、运行、环境是**宿主的领域词汇**——本仓不引入、不解释（机制面
// 不认识领域概念，与 Action.Payload 的边界同一条）。本仓给的是：
//
//   - **拼装口径**：作用域段 → 叶段，统一转义、统一分隔符，宿主不各自
//     拼字符串；
//   - **单射**：非空段全部转义（段内不可能出现分隔符 '/'），不同身份
//     （段集 × 叶段）拼不出同一个键——身份判据的前提是键不撞；
//   - **稳定性**：拼装是纯函数——同一组输入恒得同一个键。重试同一逻辑
//     动作必须给同一组段：**Attempt / 重试计数 / 时间戳 / 随机数不得入段**
//     （入段即每次重试一个新动作，幂等闸形同虚设）。
//
// 冲突判定不在这里：同 Key 异身份的处置是存储侧的 ErrActionConflict
// （判定优先于重放），本文件只负责把身份拼成一把不撞的键。
//
// # 零作用域：旧口径逐字透传
//
// 没有任何非空作用域段时，叶段**原样**返回（不转义、不加前缀）——
// 既有部署的默认键（"task:<id>"）与宿主自拼键不因本机制迁移。透传是
// 调用方自管的身份形态（与今天的宿主自拼同一信任级）；它与受管拼装
// 之间的撞键由调用方避免。
package sideeffect

import (
	"strings"
)

// ActionKeyBuilder 动作身份的作用域段拼装器。
//
// 零值不可用，经 NewActionKeyBuilder 构造。不可变：构造后拼多少次、
// 从哪个协程拼，结果都一致（重试稳定性由此成立）。
type ActionKeyBuilder struct {
	scopes []string
}

// NewActionKeyBuilder 以给定作用域段构造（段顺序即身份顺序，调用方固定
// 一种顺序并全仓只用这一种——同一逻辑动作的每次重试都必须以同一顺序
// 给出同一组段）。空段跳过（缺维度 ≠ 空维度）。
func NewActionKeyBuilder(scopes ...string) *ActionKeyBuilder {
	return &ActionKeyBuilder{scopes: scopes}
}

// NewActionKeyBuilderFor 以**动作身份**（Tenant + Scope）构造统一的键拼装器。
//
// 这是「租户 / 运行等作用域段怎么进键」的唯一入口（issue #275）：Tenant
// 排第一段、Scope 按给出顺序随后，空段跳过。宿主不要自己拼前缀——拼法
// 分散就是口径漂移的开始（与 #261 同一条纪律）。
func NewActionKeyBuilderFor(tenant string, scope ...string) *ActionKeyBuilder {
	return NewActionKeyBuilder(Action{Tenant: tenant, Scope: scope}.Scopes()...)
}

// Scopes 动作身份的作用域段：Tenant 在前（非空时），Scope 依次随后。
//
// 返回新切片（不把 Action.Scope 的底层数组外借）；空段跳过。它与
// NewActionKeyBuilderFor 共享同一段顺序——键拼装与存储身份判断读的是
// 同一组段。
func (a Action) Scopes() []string {
	out := make([]string, 0, len(a.Scope)+1)
	if a.Tenant != "" {
		out = append(out, a.Tenant)
	}
	out = append(out, a.Scope...)
	return out
}

// Key 拼装幂等键：非空作用域段（转义）/ 叶段（转义）。
//
// actionID 标识「作用域根下的哪个逻辑动作」（如任务的某个步骤）。同一
// 逻辑动作的所有重试必须用同一 actionID 与同一组作用域段——用不同的
// 等于声明「这是两个不同的动作」，闸不拦（那是调用方的语义错误）。
//
// 零作用域 ⇒ 原样返回 actionID（旧口径逐字透传，见文件头注释）。
func (b *ActionKeyBuilder) Key(actionID string) Key {
	return Key(b.build(actionID))
}

// Resource 拼装资源标识：与 Key 同一口径。资源占用面（租约判定按它
// 独占）同样需要作用域，否则跨租户同名任务互相抢资源。
func (b *ActionKeyBuilder) Resource(resourceID string) string {
	return b.build(resourceID)
}

// build 拼装：非空段全转义、'/' 只作分隔符、叶段恒为末段。
func (b *ActionKeyBuilder) build(leaf string) string {
	segs := make([]string, 0, len(b.scopes)+1)
	for _, s := range b.scopes {
		if s == "" {
			continue
		}
		segs = append(segs, escapeKeySegment(s))
	}
	if len(segs) == 0 {
		return leaf
	}
	segs = append(segs, escapeKeySegment(leaf))
	return strings.Join(segs, "/")
}

// escapeKeySegment 把一段编码成「无分隔符」的单段：只保留
// [A-Za-z0-9_-]，其余字节按 %XX 转义（含 '%' 本身）。编码是单射——
// 不同段名拼不出同一个键，段内也无法伪造分隔符。与 coord 的租户段
// 转义同一方案（两个面的作用域口径必须同构，撞键分析才共享同一份结论）。
func escapeKeySegment(s string) string {
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
