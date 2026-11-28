// judge.go —— docs/DELIVERY-SEMANTICS.md 的解析与判定（判据纯函数，不读文件系统）。
//
// # 这条判据补的是什么缺口
//
// 评审（2026-11-27 §3.3 问题三）：不要把所有能力统一包装成 Exactly Once——
// 分级要成文，失真要会红。此前四档语义散在 HARNESS-BENCHMARK §五与各包注释里，
// 「哪个能力面承诺哪一档」没有单源表；把 at-most-once 的上界说成 exactly-once
// 也没有任何机器判据会拦。
//
// # 三类判据（失败形态是变红）
//
//  1. **表结构与覆盖**：能力面表覆盖全部 Stable 包（包列 token 对账 api-lifecycle）、
//     档位在四档值域（或显式「不适用（非交付面）」）、交付行 ≥6、各格非空、
//     登记的判据测试真实存在；
//  2. **白名单有理由**：白名单每行理由非空、文件真实存在（僵尸白名单即红）；
//  3. **失真扫描**：根 md / docs/*.md / 生产 .go 里的 exactly-once 字样，白名单外
//     且拒绝语境窗口（±2 行）内无拒绝标记 ⇒ 红并给 file:line。
//
// # 判据与数据源分离
//
// 判定是纯函数（登记面 + 注入事实 ⇒ 结论）；文件扫描、api-lifecycle 读取、测试
// 名索引由 cmd/semanticscheck 注入 ⇒ 用例可确定性测、不联网。
package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// 四档关键词（档位格必须至少命中一个才算交付行）。
var tierKeywords = []string{"at-most-once", "at-least-once", "effectively-once", "idempotent"}

// notApplicableTier 非交付面的显式档位（不许留白——不适用也要写出来）。
const notApplicableTier = "不适用（非交付面）"

// 拒绝语境标记（命中行 ±2 行窗口内出现任一 ⇒ 放行）。
//
// 这份清单本身登记在 docs/DELIVERY-SEMANTICS.md §三（判据口径单源，改清单先改文档）。
var rejectionMarkers = []string{
	"不承诺", "不提供", "不构成", "拒绝", "而非", "尚未", "不是",
	"接线责任", "要求下游", "说成", "失真",
}

// exactlyOnceRe 失真扫描的目标字样（大小写不敏感，hyphen 与空格两形态）。
var exactlyOnceRe = regexp.MustCompile(`(?i)exactly[- ]once`)

// testRefRe 判据列里的测试指针（dir/TestName 形态，Test 大写开头）。
var testRefRe = regexp.MustCompile(`[A-Za-z0-9_][A-Za-z0-9_./-]*\.Test[A-Za-z0-9_]+`)

// updatedRe 抓版本戳行。
var updatedRe = regexp.MustCompile(`(?m)^updated:\s*(\S+)\s*$`)

// 表头（按逐字表头定位）。
const (
	capTableHeader = "| 能力面 | 包 | 承诺档位 | 判定机制 | 现有判据 |"
	wlTableHeader  = "| 文件 | 理由 |"
)

// CapRow 能力面表一行。
type CapRow struct {
	Face      string
	Pkgs      string
	Tier      string
	Mechanism string
	Judgments string
}

// WLRow 白名单一行。
type WLRow struct {
	File   string
	Reason string
}

// Registry 解析后的登记面。
type Registry struct {
	Updated   string
	Rows      []CapRow
	Whitelist []WLRow
}

// Hit 一条失真扫描命中（含 ±2 行窗口文本，判定在纯函数侧做）。
type Hit struct {
	File   string
	Line   int
	Text   string
	Window []string
}

// Facts 仓内事实（调用方注入）。
type Facts struct {
	Registered map[string]bool // api-lifecycle 全集（pkg → true）
	Stable     map[string]bool // api-lifecycle Stable 子集
	Tests      map[string]bool // dir.TestName 全键集合
	Whitelist  map[string]bool // 白名单文件真实存在（相对仓库根 → true）
	Hits       []Hit           // 失真扫描命中
}

// Result 一次判定的结论。
type Result struct {
	Problems  []string
	Rows      int
	Delivery  int
	StableHit int
	Scanned   int
}

// Judge 判登记面与仓内事实是否一致（纯函数）。
func Judge(reg *Registry, f Facts) Result {
	var res Result
	if reg == nil || f.Registered == nil || f.Stable == nil || f.Tests == nil || f.Whitelist == nil {
		res.Problems = append(res.Problems,
			"交付语义登记面或仓内事实缺失：判据跑不起来（跑不起来的门禁等于没有门禁）")
		return res
	}
	res.Rows = len(reg.Rows)
	res.Scanned = len(f.Hits)

	res.Problems = append(res.Problems, checkShape(reg)...)
	res.Problems = append(res.Problems, checkCoverage(reg, f, &res)...)
	res.Problems = append(res.Problems, checkWhitelist(reg, f)...)
	res.Problems = append(res.Problems, checkClaims(reg, f)...)
	return res
}

// checkShape 判登记面自身的结构。
func checkShape(reg *Registry) []string {
	var problems []string
	if _, err := time.Parse("2006-01-02", reg.Updated); err != nil {
		problems = append(problems, fmt.Sprintf(
			"交付语义登记面的 Updated %q 不是 YYYY-MM-DD：登记面自己的版本戳都不可信", reg.Updated))
	}
	if len(reg.Rows) == 0 {
		return append(problems, "能力面表一行都没有（表头下没有数据行）")
	}
	for i, r := range reg.Rows {
		where := fmt.Sprintf("第 %d 行（%s）", i+1, orDash(r.Face))
		for _, c := range []struct{ name, val string }{
			{"能力面", r.Face}, {"包", r.Pkgs}, {"承诺档位", r.Tier},
			{"判定机制", r.Mechanism}, {"现有判据", r.Judgments},
		} {
			if strings.TrimSpace(c.val) == "" {
				problems = append(problems, fmt.Sprintf(
					"能力面表%s的%s格为空：零空格是这张表的底线——没有判据面就写「—」，留白即红", where, c.name))
			}
		}
	}
	for _, w := range reg.Whitelist {
		if strings.TrimSpace(w.File) == "" {
			problems = append(problems, "白名单有空文件条目：豁免必须指名道姓")
		}
		if strings.TrimSpace(w.Reason) == "" {
			problems = append(problems, fmt.Sprintf(
				"白名单条目 %s 没有理由：豁免必须写理由，与 policy 例外同纪律", orDash(w.File)))
		}
	}
	return problems
}

// checkCoverage 判 Stable 全覆盖、包 token 合法、档位值域、交付行 ≥6、判据存在。
func checkCoverage(reg *Registry, f Facts, res *Result) []string {
	var problems []string
	stableSeen := map[string]bool{}
	delivery := 0
	for _, r := range reg.Rows {
		tokens := pkgTokens(r.Pkgs)
		for _, t := range tokens {
			if !f.Registered[t] {
				problems = append(problems, fmt.Sprintf(
					"能力面「%s」的包列含未登记包 %s：包必须挂在 api-lifecycle 清单上，不许自造", orDash(r.Face), t))
				continue
			}
			if f.Stable[t] {
				stableSeen[t] = true
			}
		}
		if r.Tier == notApplicableTier {
			continue
		}
		delivery++
		if !hasTierKeyword(r.Tier) {
			problems = append(problems, fmt.Sprintf(
				"能力面「%s」的档位 %q 不在四档（at-most-once / at-least-once / effectively-once / idempotent-execution）里——档位是承诺，值域外的取值宿主读不懂",
				orDash(r.Face), r.Tier))
		}
		for _, ref := range testRefRe.FindAllString(r.Judgments, -1) {
			if !f.Tests[ref] {
				problems = append(problems, fmt.Sprintf(
					"能力面「%s」登记的判据 %s 在仓内不存在：清单里指着一条没人守的测试，比没有清单更坏",
					orDash(r.Face), ref))
			}
		}
	}
	res.Delivery = delivery
	if delivery < 6 {
		problems = append(problems, fmt.Sprintf(
			"交付语义行只有 %d 行（须 ≥6）：评审点名的六个能力面（事件/副作用/终态/快照/认领/配额）一个都不许少", delivery))
	}
	for pkg := range f.Stable {
		if !stableSeen[pkg] {
			problems = append(problems, fmt.Sprintf(
				"Stable 包 %s 没出现在能力面表的包列：覆盖全部 Stable 契约面是这张表的出口条件", pkg))
		}
	}
	res.StableHit = len(stableSeen)
	return problems
}

// checkWhitelist 判白名单文件真实存在（僵尸白名单即红）。
func checkWhitelist(reg *Registry, f Facts) []string {
	var problems []string
	for _, w := range reg.Whitelist {
		if !f.Whitelist[w.File] {
			problems = append(problems, fmt.Sprintf(
				"白名单条目 %s 指向不存在的文件：僵尸白名单比没有白名单更坏（它让豁免面静默扩大）", w.File))
		}
	}
	return problems
}

// checkClaims 判每条失真命中是否放行（白名单 or 拒绝语境窗口）。
func checkClaims(reg *Registry, f Facts) []string {
	var problems []string
	wl := map[string]bool{}
	for _, w := range reg.Whitelist {
		wl[w.File] = true
	}
	for _, h := range f.Hits {
		if wl[h.File] {
			continue
		}
		if windowHasMarker(h.Window) {
			continue
		}
		problems = append(problems, fmt.Sprintf(
			"失真声明：%s:%d 出现 exactly-once 字样——既不在白名单也不在拒绝语境（±2 行窗口无拒绝标记）：%s",
			h.File, h.Line, strings.TrimSpace(h.Text)))
	}
	return problems
}

// windowHasMarker 判 ±2 行窗口内有无拒绝标记。
func windowHasMarker(window []string) bool {
	for _, line := range window {
		for _, m := range rejectionMarkers {
			if strings.Contains(line, m) {
				return true
			}
		}
	}
	return false
}

// hasTierKeyword 判档位格是否命中四档任一关键词。
func hasTierKeyword(tier string) bool {
	t := strings.ToLower(tier)
	for _, k := range tierKeywords {
		if strings.Contains(t, k) {
			return true
		}
	}
	return false
}

// pkgTokens 把包列切成单包 token（按顿号/逗号/加号/空白切；斜杠是包路径的一部分，不切）。
func pkgTokens(cell string) []string {
	parts := strings.FieldsFunc(cell, func(r rune) bool {
		return r == '、' || r == ',' || r == '+' || r == ' ' || r == '（' || r == '）'
	})
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// 只认像包路径的 token（含斜杠或全小写标识）——中文说明词不参与对账。
		if strings.Contains(p, "/") || isLowerIdent(p) {
			out = append(out, p)
		}
	}
	return out
}

// isLowerIdent 判是否全小写标识（包名形态）。
func isLowerIdent(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return len(s) > 0
}

// orDash 报错定位用的兜底显示。
func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
