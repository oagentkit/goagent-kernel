// tenantgate_selfcheck.go —— tenant_gate 段的形状自校验。
//
// 这一段是**形状声明**：授权上下文类型名 / 租户域字段名 / 扫描面。缺了任一项，
// 执行体就没有判定面——要么对每个函数报「无证据」（假红一片），要么静默不判
// （假绿）。两种失败形态都必须在这里变成红，而不是留给宿主去猜。
package archcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// tenantGateSelfCheck 校验 tenant_gate 段的形状完整性。
func tenantGateSelfCheck(root, policyPath string, tg *TenantGate) []Violation {
	if tg == nil {
		return nil
	}
	var out []Violation
	add := func(format string, args ...any) {
		out = append(out, Violation{
			Rule: selfCheckRuleID, Path: policyPath, Text: fmt.Sprintf(format, args...),
			Kind: "policy_self_check",
		})
	}
	if tg.Severity != "" && tg.Severity != "error" && tg.Severity != "warn" {
		add("tenant_gate.severity %q 非法（只允许 error / warn / 缺省）", tg.Severity)
	}
	if !tg.on() {
		return out
	}
	if len(tg.Roots) == 0 {
		add("tenant_gate 已启用但没有 roots：服务层扫描面为空，门禁一条函数都不判")
	}
	if len(tg.AuthContextTypes) == 0 {
		add("tenant_gate 已启用但没有 auth_context_types：认不出授权上下文形参，门禁一条判据都触发不了")
	}
	if len(tg.TenantFields) == 0 {
		add("tenant_gate 已启用但没有 tenant_fields：租户域类型推导面为空，判据 1 永远不触发")
	}
	for _, r := range append(append([]string{}, tg.Roots...), tg.TenantTypeRoots...) {
		if r == "" {
			add("tenant_gate 的扫描面里有空条目（空字符串不扫任何目录）")
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(r))); err != nil {
			add("tenant_gate 的扫描面 %q 不存在或无法读取（%v）：那棵树对租户门完全隐形", r, err)
		}
	}
	for _, v := range append(append([]string{}, tg.AuthContextTypes...), tg.TenantFields...) {
		if strings.TrimSpace(v) == "" {
			add("tenant_gate 的类型/字段清单里有空条目（空条目只是让清单看起来更长）")
		}
	}
	for _, s := range tg.SharedSurfaces {
		if strings.TrimSpace(s.Dir) == "" || strings.TrimSpace(s.Name) == "" {
			add("tenant_gate.shared_surfaces 有缺少 dir 或 name 的登记项：无法做调用匹配与存在性核对")
		}
	}
	if p := strings.TrimSpace(tg.TenantTablesManifest); p != "" {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
			add("tenant_gate.tenant_tables_manifest %q 不存在或无法读取（%v）：清单读不到，双向核对一条都判不了", p, err)
		}
	}
	return out
}
