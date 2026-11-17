// policy_exception.go —— policy 的 exceptions 段（例外登记的声明面）。
//
// 单独成文件的原因同 policy_seams.go：policy.go 已经顶到 max_file_lines（400 行），
// 而它还要继续长出声明段。划界按用途：这里是「精确豁免」的声明面，
// 不认识任何判定逻辑（豁免的判定在 selfcheck.go）。

package archcheck

// Exception 是例外登记：精确豁免某条规则的某处违反。
//
// 豁免必须写 until（到期日）与 removal（拆除条件）——没有到期日的豁免等于永久豁免，
// 没有拆除条件的豁免是赖账。两者都由 policy 自校验强制。
type Exception struct {
	Rule      string `yaml:"rule"`
	Path      string `yaml:"path"`
	Line      int    `yaml:"line"`
	Violation string `yaml:"violation"`
	Reason    string `yaml:"reason"`
	Until     string `yaml:"until"`
	Removal   string `yaml:"removal"`
}

// facadeRuleID 门面包不可变规则的 ID。所有门面包共用一条规则 ID：它们守的是同一条
// 纪律（转发到上游、不得在本仓长实现），逐包差异体现在 violation 的 path 与 text 里。
const facadeRuleID = "harness-facade-stays-facade"
