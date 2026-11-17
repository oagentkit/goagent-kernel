// seam_selfcheck.go —— policy 的 seams 段自校验（判据 C）。
//
// # 它判什么
//
// 声明面的**形状**：段在不在、id 重不重复、id 长得对不对。
//
// 为什么不判「登记的接缝是不是真的存在」——那是判据 A（完备性）的事，
// 由接缝门禁本体判（它有扫描器）。这里只判「这张表本身合不合法」。
//
// # 为什么放在 policy 自校验这一层
//
// 与 modules 段的包根、facades 段的目录同一处置：**恒开**。
// 一个拼错的 id（多一个空格、少一段路径）会让清单里那一条永远匹配不上，
// 于是「该接缝对门禁隐形」——而门禁仍然绿着。

package archcheck

import (
	"fmt"
	"strings"
)

// seamSelfCheck 校验 policy 的 seams 段本身。
func seamSelfCheck(policyPath string, pol *Policy) []Violation {
	var out []Violation
	add := func(format string, args ...any) {
		out = append(out, Violation{
			Rule: selfCheckRuleID,
			Path: policyPath,
			Text: fmt.Sprintf(format, args...),
			Kind: "policy_self_check",
		})
	}
	if pol.Seams == nil {
		add("policy 缺少 seams 段：接缝契约门禁没有声明面——" +
			"判据 A（清单完备）与判据 B（契约成文）都无从判，接缝清单会变成死条文；" +
			"若不使用这条门禁，请显式写 seams: {enabled: false}")
		return out
	}
	seen := map[string]bool{}
	for _, r := range pol.Seams.Registry {
		id := strings.TrimSpace(r.ID)
		switch {
		case id == "":
			add("seams.registry 里有空 id（无法定位是哪条接缝）")
			continue
		case id != r.ID:
			add("seams.registry 的 id %q 前后有空白：id 必须逐字等于扫描器产出的接缝标识", r.ID)
			continue
		case strings.ContainsAny(id, " \t\n"):
			add("seams.registry 的 id %q 含空白：id 必须逐字等于扫描器产出的接缝标识", r.ID)
			continue
		case !strings.Contains(id, "."):
			add("seams.registry 的 id %q 不合法：形状应为 <包路径>.<名字>"+
				"（方法与结构体字段为 <包路径>.<类型>.<名字>）", r.ID)
			continue
		}
		if seen[id] {
			add("seams.registry 的 id %q 重复登记：清单里有一份是死的，且定位会指错", id)
		}
		seen[id] = true
	}
	return out
}
