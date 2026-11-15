package dispatch

// 改派策略的四级数据化优先级（issue #169）：
//
//	条目 dispatch  >  表级 dispatch_defaults  >  conf 旋钮  >  代码缺省
//
// 解析是**纯函数**（Resolve）：宿主把三层声明原样交进来，本包逐字段回落。
// 指针字段表达「本层未声明」——0 与 false 是**显式声明**（关闭改派 / 不跨域），
// 不是「没说」；裸 int/bool 表达不了这个区别，宿主的 yaml 解析也靠指针区分。

const (
	// DefaultSameDomain 代码缺省：同域改派一次（存量行为）。
	DefaultSameDomain = 1
	// DefaultCrossDomain 代码缺省：不跨域。
	DefaultCrossDomain = false
)

// Policy 改派策略的**一层**声明（条目层 / 表级层 / conf 层各一份）。
//
// 字段为 nil 表示该层**未声明**，回落到下一级；非 nil 即使是 0/false 也是
// 显式声明（同域 0 = 禁用改派；跨域 false = 显式不跨域）。
type Policy struct {
	// SameDomain 同域改派次数上限；0=禁用改派；nil=回落下一级。
	SameDomain *int
	// CrossDomain 瞬态错误在同域改派耗尽后允许跨域兜底一次；权限/凭证类
	// 永不跨域（fatal 在环里直接终态，本开关管不到它）。nil=回落下一级。
	CrossDomain *bool
}

// Effective 解析指针语义：本层声明的字段优先，未声明的字段回落 def。
//
// 逐字段独立回落——同一条目可以只覆盖 SameDomain 而让 CrossDomain 继续回落。
func (p Policy) Effective(def Policy) Policy {
	out := Policy{SameDomain: def.SameDomain, CrossDomain: def.CrossDomain}
	if p.SameDomain != nil {
		out.SameDomain = p.SameDomain
	}
	if p.CrossDomain != nil {
		out.CrossDomain = p.CrossDomain
	}
	return out
}

// Resolved 把落定后的指针解析成具体值；未声明的字段取代码缺省
// （同域一次 / 不跨域）。负数次数按 0（禁用）钳位——负数没有语义，
// 放行会让「写错了的配置」变成一个比 0 更难解释的值。
func (p Policy) Resolved() (sameDomain int, crossDomain bool) {
	n := DefaultSameDomain
	if p.SameDomain != nil {
		n = *p.SameDomain
	}
	if n < 0 {
		n = 0
	}
	if p.CrossDomain != nil {
		return n, *p.CrossDomain
	}
	return n, DefaultCrossDomain
}

// Resolve 四级数据化优先级的解析纯函数：条目 > 表级 > conf 旋钮 > 代码缺省。
//
// 三层都可以是零值 Policy（该层什么都不声明）；解析结果与逐层
// Effective 叠加后取 Resolved() 等价——写成一条链是为了让「优先级顺序」
// 只有一处真相（顺序写错会在 Resolve 的测试里当场红）。
func Resolve(entry, defaults, conf Policy) (sameDomain int, crossDomain bool) {
	return entry.Effective(defaults.Effective(conf)).Resolved()
}
