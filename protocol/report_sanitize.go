package protocol

// 报告脚手架净化（批次三百三十九，解析出口服务端单点）：多轮接力场景模型会把组装
// prompt 的脚手架原文复述进报告字段——<system-reminder source="conversation_history">
// 跨轮上下文块与【历史对话】【调度上下文】等结构段（sess-1013-d58e4c6v 链终报
// 「现象」字段实弹；更早 sess-1005-afzbzq9q 促成批157 前端净化器）。前端净化器只管
// 展示面，落库原文仍带着脚手架流进记忆蒸馏/知识飞轮/接力注入（下一轮【上一轮系统
// 结论】原样回喂）/API 消费方——数据面卫生必须在解析出口收口。前端净化器保留
//（历史存量报告已落库不可再生，展示面仍需兜底）；纯渲染修饰（专家分段/标题降级/
// Setext 中和）属展示语义，不在本函数重复。
//
// 骨架标记词表**单一来源**是同目录的 scaffold_markers.json（机器可读，宿主组装侧
// 与前端净化器直接消费）；本文件嵌入该清单并用 ScaffoldMarkers() 导出副本，
// 不再维持三处手抄——新增标记只改清单，净化判据与导出自动跟随（issue #285）。

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// scaffoldMarkersJSON 机器可读的脚手架标记清单：**词表的唯一来源**。
//
// 宿主组装侧与前端无法 import 本包的 Go 变量，故词表不能只活成包内私有 var——
// 那样三处手抄，新增标记时没有任何判据会红（issue #285）。清单文件同时被本包
// 嵌入（净化面直接消费）与导出（ScaffoldMarkers）⇒ 「新增标记只改一处」。
//
//go:embed scaffold_markers.json
var scaffoldMarkersJSON []byte

// ScaffoldMarkers 返回脚手架标记词表的副本（升序不保证：沿用清单顺序）。
//
// 宿主（组装 prompt 的调度侧）与前端净化器应从这里/清单文件取词表，不要手抄：
// 词表与净化判据是同一份，新增标记只改 scaffold_markers.json。
func ScaffoldMarkers() []string { return slices.Clone(scaffoldMarkers) }

// scaffoldMarkers 净化面使用的词表，由嵌入清单解析而来。
var scaffoldMarkers = mustScaffoldMarkers(scaffoldMarkersJSON)

// mustScaffoldMarkers 解析并校验嵌入清单。清单是编译期常量：坏清单属于构建缺陷，
// 在包初始化时大声失败，而不是让净化面静默退化成空词表。
func mustScaffoldMarkers(raw []byte) []string {
	var markers []string
	if err := json.Unmarshal(raw, &markers); err != nil {
		panic(fmt.Sprintf("protocol: 脚手架标记清单非法: %v", err))
	}
	if len(markers) == 0 {
		panic("protocol: 脚手架标记清单为空")
	}
	seen := make(map[string]struct{}, len(markers))
	for _, m := range markers {
		if strings.TrimSpace(m) == "" {
			panic("protocol: 脚手架标记清单含空串")
		}
		if _, dup := seen[m]; dup {
			panic(fmt.Sprintf("protocol: 脚手架标记 %q 重复", m))
		}
		seen[m] = struct{}{}
	}
	return markers
}

// scaffoldFastRunes 标记首字符去重集合：净化入口的快路径判据由词表派生，
// 不再手写 "【"（新增一个不以 【 开头的标记时，快路径也要自动覆盖到）。
var scaffoldFastRunes = func() []rune {
	seen := map[rune]struct{}{}
	var out []rune
	for _, m := range scaffoldMarkers {
		r := []rune(m)[0]
		if _, ok := seen[r]; !ok {
			seen[r] = struct{}{}
			out = append(out, r)
		}
	}
	return out
}()

// mayContainScaffold 文本是否可能含任一骨架标记（快路径，宁多勿漏）。
func mayContainScaffold(s string) bool {
	for _, r := range scaffoldFastRunes {
		if strings.ContainsRune(s, r) {
			return true
		}
	}
	return false
}

var (
	// system-reminder 块（含未闭合兜底：剥到串尾——闭环缺失时宁多勿漏，骨架不该进报告）。
	reminderBlockRe = regexp.MustCompile(`(?is)<system-reminder[^>]*>.*?</system-reminder>|<system-reminder[^>]*>.*$`)
	// 同段混排截断终点（与前端 markerRe 的 lookahead 边界同语义——Go regexp 无
	// 前瞻，改手写扫描，见 cutInlineMarkers）：空行/markdown 标题行/字段标记行。
	inlineBoundaryRe = regexp.MustCompile(`\n[ \t]*\n|\n#{1,6}[ \t]|\n\*\*`)
	blankParaRe      = regexp.MustCompile(`\n{2,}`)
	multiBlankRe     = regexp.MustCompile(`\n{3,}`)
)

// cutInlineMarkers 同段混排兜底：行内出现骨架标记时，从标记处截到下一空行/标题/
// 字段标记（保留边界本身）或串尾。逐处循环直至无标记。
func cutInlineMarkers(s string) string {
	for {
		idx, mlen := firstScaffoldMarker(s)
		if idx < 0 {
			return s
		}
		rest := s[idx+mlen:]
		end := len(rest)
		if loc := inlineBoundaryRe.FindStringIndex(rest); loc != nil {
			end = loc[0]
		}
		s = s[:idx] + rest[end:]
	}
}

// firstScaffoldMarker 首个骨架标记的位置与长度（无命中返回 -1）。
func firstScaffoldMarker(s string) (int, int) {
	best, bestLen := -1, 0
	for _, m := range scaffoldMarkers {
		if i := strings.Index(s, m); i >= 0 && (best < 0 || i < best) {
			best, bestLen = i, len(m)
		}
	}
	return best, bestLen
}

// sanitizeScaffoldText 单字段净化：reminder 块剔除 → 骨架段按空行分段剔除 →
// 同段混排标记截断 → 残留空行收敛。无脚手架快路径零开销。
func sanitizeScaffoldText(s string) string {
	if s == "" || (!strings.Contains(s, "<system-reminder") && !mayContainScaffold(s)) {
		return s
	}
	out := reminderBlockRe.ReplaceAllString(s, "")
	if blankParaRe.MatchString(out) {
		paras := blankParaRe.Split(out, -1)
		kept := make([]string, 0, len(paras))
		for _, p := range paras {
			if isScaffoldPara(p) {
				continue
			}
			kept = append(kept, p)
		}
		out = strings.Join(kept, "\n\n")
	} else if isScaffoldPara(out) {
		out = ""
	}
	out = cutInlineMarkers(out)
	out = multiBlankRe.ReplaceAllString(out, "\n\n")
	return strings.TrimSpace(out)
}

// isScaffoldPara 段首（允许前导空白）命中任一骨架标记。
func isScaffoldPara(p string) bool {
	t := strings.TrimLeft(p, " \t\n\r")
	for _, m := range scaffoldMarkers {
		if strings.HasPrefix(t, m) {
			return true
		}
	}
	return false
}

// sanitizeScaffold 报告全字段净化（自由文本面）：symptom/conclusion/impact/risk/
// rollback/summary/domain + evidence snippet/interpretation + needs_followup/
// impact_scopes 列表项。枚举/slug/命令字段（decision/executed_steps/recommendation.
// steps）不动——形态由各自校验面治理，脚手架混入命令属审批闸职责。
func sanitizeScaffold(r *Report) *Report {
	if r == nil {
		return r
	}
	r.AgentName = sanitizeScaffoldText(r.AgentName)
	r.Domain = sanitizeScaffoldText(r.Domain)
	r.Symptom = sanitizeScaffoldText(r.Symptom)
	r.Conclusion = sanitizeScaffoldText(r.Conclusion)
	r.Impact = sanitizeScaffoldText(r.Impact)
	r.Risk = sanitizeScaffoldText(r.Risk)
	r.Rollback = sanitizeScaffoldText(r.Rollback)
	r.Summary = sanitizeScaffoldText(r.Summary)
	for i := range r.Evidence {
		r.Evidence[i].Snippet = sanitizeScaffoldText(r.Evidence[i].Snippet)
		r.Evidence[i].Interpretation = sanitizeScaffoldText(r.Evidence[i].Interpretation)
	}
	r.NeedsFollowup = sanitizeScaffoldList(r.NeedsFollowup)
	r.ImpactScopes = sanitizeScaffoldList(r.ImpactScopes)
	return r
}

// sanitizeScaffoldList 列表项净化：净化后空项剔除（整项都是脚手架的形态）。
func sanitizeScaffoldList(l StringList) StringList {
	if len(l) == 0 {
		return l
	}
	out := make(StringList, 0, len(l))
	for _, item := range l {
		if s := sanitizeScaffoldText(item); s != "" {
			out = append(out, s)
		}
	}
	return out
}
