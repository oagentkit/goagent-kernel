// parse.go —— 台账文本的容错解析。
//
// 解析口径全在这一份里：判据（cmd/ledgercheck）只消费本包派生出的事实，
// 不自己再读一遍 markdown——两处各读一遍，结论就会分叉，而分叉的方向
// 恰好是「门禁判的和台账写的不是一套」。
package ledgerstatus

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

const (
	// 登记列的表头词：这些列里的 #N 是「被登记的单号」。
	//
	// 为什么是词表而不是「任何含 #N 的列」：台账里既有登记列（单号 / 单 /
	// 已落单 / 结论单），也有正文明明在讲证据的列（§2 的「证据」列里就有
	// 「issue #183」）。把两者混为一谈，登记面就退化成「文档里出现过的地方」，
	// 那不叫登记面。
	headerIssueNo = "单号"
	headerIssue   = "单"
	headerLanded  = "已落单"
	headerResult  = "结论单"

	// headerStatus 状态列的表头词。
	headerStatus = "状态"

	// 状态标记词：正文行 / 表格非登记列里出现它，同行的单号继承该状态。
	//
	// 为什么词表这么短：标记词越宽，误判越多——台账正文里「已坐实」「已达成」
	// 「已落地」满地都是，把它们当状态标记会凭空造出状态声明，而状态声明
	// 是用来判矛盾的（假的矛盾比没矛盾更贵）。
	markerClosed  = "已合入"
	markerClosed2 = "已关闭"
	markerOpen    = "仍开着"

	// truncateWidth 展示用的截断宽度。
	truncateWidth = 80
)

var (
	// numberRe 单号形态：# 后接数字。
	numberRe = regexp.MustCompile(`#\d+`)
	// prWord PR 词（大小写不敏感）。
	prWord = []rune{'P', 'R'}
)

// tableBlock 一段连续的表格行。
type tableBlock struct {
	start, end int // 行号（0 起，右开）
}

// findTableBlocks 找出文档里所有表格块（连续的以 | 开头的行）。
func findTableBlocks(lines []string) []tableBlock {
	var out []tableBlock
	for i := 0; i < len(lines); {
		if !isRowLine(lines[i]) {
			i++
			continue
		}
		j := i
		for j < len(lines) && isRowLine(lines[j]) {
			j++
		}
		out = append(out, tableBlock{start: i, end: j})
		i = j
	}
	return out
}

// isRowLine 判断一行是不是表格行。
func isRowLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "|")
}

// splitRow 拆表格行：去掉首尾竖线后按 | 拆列。
//
// 不处理转义竖线——台账里没有；真出现了也只是这一列的文本被拆开，
// 不会让整份台账读不出来。
func splitRow(line string) []string {
	s := strings.TrimSpace(line)
	s = strings.TrimPrefix(s, "|")
	s = strings.TrimSuffix(s, "|")
	parts := strings.Split(s, "|")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.TrimSpace(p)
	}
	return out
}

// isSeparatorRow 判断是不是表头下面的分隔行（|---|---|）。
func isSeparatorRow(line string) bool {
	if !isRowLine(line) {
		return false
	}
	for _, c := range splitRow(line) {
		if c == "" {
			return false
		}
		for _, r := range c {
			if r != '-' && r != ':' {
				return false
			}
		}
	}
	return true
}

// parseTable 解析一个表格块。
func (l *Ledger) parseTable(lines []string, b tableBlock) {
	header := splitRow(lines[b.start])
	regIdx := map[int]bool{}
	statusIdx := -1
	for i, h := range header {
		switch h {
		case headerStatus:
			statusIdx = i
		case headerIssueNo, headerIssue, headerLanded, headerResult:
			regIdx[i] = true
		}
	}
	for r := b.start + 1; r < b.end; r++ {
		row := lines[r]
		if isSeparatorRow(row) {
			continue
		}
		cells := splitRow(row)
		if len(cells) != len(header) {
			// 列数不齐不吞掉：台账是散文，表格随时可能被手改歪，
			// 而「解析不出来却不说话」= 判据静默失效。
			l.problems = append(l.problems, Problem{
				File: l.file, Line: r + 1, Kind: ProblemTableShape,
				Message: fmt.Sprintf("表格行列数与表头不一致（表头 %d 列，本行 %d 列）：%s",
					len(header), len(cells), truncate(row, truncateWidth)),
			})
		}
		l.parseRow(r, cells, regIdx, statusIdx)
	}
}

// parseRow 解析表格里的一行。
func (l *Ledger) parseRow(line int, cells []string, regIdx map[int]bool, statusIdx int) {
	statusText := ""
	if statusIdx >= 0 && statusIdx < len(cells) {
		statusText = cells[statusIdx]
	}
	// 只有**这张表真的有状态列**时，这一行才对状态表态。没有状态列的表
	// （各轮的「面 → 单」表）压根没谈状态：让它们表态就是凭空造出声明，
	// 而状态声明会被判据 1 拿去判矛盾——假矛盾比没矛盾更贵。
	rowStatus := StatusUnknown
	if statusIdx >= 0 {
		rowStatus = statusFromCell(statusText)
	}
	closing := closingRefFromText(statusText)
	for idx, cell := range cells {
		if regIdx[idx] {
			for _, n := range issueNumbersIn(cell) {
				l.add(Occurrence{
					Number: n, File: l.file, Line: line + 1,
					Kind: OccurrenceRegistry, Status: rowStatus,
					ClosingRef: closing, Text: truncate(cell, truncateWidth),
				})
			}
			continue
		}
		if idx == statusIdx {
			// 状态列里的号是「关闭 PR 号」，不是单号引用。它挂在本行登记的
			// 单号上（ClosingRef），不进单号视图——否则 PR 号会冒充单号。
			rest := statusText
			if closing != "" {
				rest = strings.Replace(rest, closing, "", 1)
			}
			for _, n := range issueNumbersIn(rest) {
				l.add(Occurrence{
					Number: n, File: l.file, Line: line + 1,
					Kind: OccurrenceReference, Status: StatusUnknown,
					Text: truncate(cell, truncateWidth),
				})
			}
			continue
		}
		cellStatus := statusForLine(cell)
		for _, n := range issueNumbersIn(cell) {
			l.add(Occurrence{
				Number: n, File: l.file, Line: line + 1,
				Kind: OccurrenceReference, Status: cellStatus,
				Text: truncate(cell, truncateWidth),
			})
		}
	}
}

// parseProse 解析一行散文。
func (l *Ledger) parseProse(lines []string, idx int, line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	status := statusForLine(line)
	for _, n := range issueNumbersIn(line) {
		l.add(Occurrence{
			Number: n, File: l.file, Line: idx + 1,
			Kind: OccurrenceReference, Status: status,
			Text: truncate(strings.TrimSpace(line), truncateWidth),
		})
	}
}

// statusFromText 从一段文本里读出状态声明。
//
// 一行里同时出现两种标记时**判不出**（返回 StatusUnknown）：那种行多半是
// 散文在同时讲两件事，猜一个就是替台账做决定。
func statusFromText(text string) Status {
	closed := strings.Contains(text, markerClosed) || strings.Contains(text, markerClosed2)
	open := strings.Contains(text, markerOpen)
	switch {
	case closed && open:
		return StatusUnknown
	case closed:
		return StatusClosed
	case open:
		return StatusOpen
	default:
		return StatusUnknown
	}
}

// statusFromCell 读**状态列**的声明。
//
// 口径（台账 §9 主表的约定）：状态列**空着** ⇒ 仍开着；写了「已合入」⇒
// 已清偿；写了别的（且不含「已合入」）⇒ 仍开着。
//
// 为什么空列也算一声声明：那张表就是「这一列写没写清」的登记表——
// 空着是「还没合入」的**明写**，不是「没说话」。判据 1（同一单号各处状态
// 必须一致）要靠这个才能把「表里说开着」与「别处说开着」对上。
func statusFromCell(text string) Status {
	if strings.TrimSpace(text) == "" {
		return StatusOpen
	}
	return statusFromText(text)
}

// closingRefFromText 从状态列文本里取「关闭 PR 号」的**原文**。
//
// 取原文而不是数字是有意的：判据「关闭 PR 号必须形如 #N」要判的就是这个
// 原文——解析时就把不合法的号丢掉，门禁就永远看不到它。
func closingRefFromText(text string) string {
	if i := strings.IndexByte(text, '('); i >= 0 {
		if j := strings.IndexByte(text[i+1:], ')'); j >= 0 {
			return strings.TrimSpace(text[i+1 : i+1+j])
		}
		return strings.TrimSpace(text[i+1:])
	}
	if loc := numberRe.FindString(text); loc != "" {
		return loc
	}
	return ""
}

// issueNumbersIn 取出文本里的单号引用（排除 PR 引用）。
func issueNumbersIn(text string) []int {
	prs := prRefsIn(text)
	var out []int
	seen := map[int]bool{}
	for _, m := range numberRe.FindAllStringIndex(text, -1) {
		n, err := strconv.Atoi(text[m[0]+1 : m[1]])
		if err != nil || prs[n] || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// issueMentionsIn 返回文本里非 PR 单号的**全部出现**（含同一单号重复出现、
// 不去重）。
//
// 与 issueNumbersIn（去重）分开是有意的：判「这一行的状态词修饰谁」要看的是
// **出现次数**，不是不同单号的个数——
//
//	——#411 就是这条的实例。**本轮动了一处**（#411 的用例换成……），**仍开着**——……
//
// 这一行里 #411 只有一个**编号**，却出现在两处语境里（前半句讲它是什么、
// 后半句讲「这条建议」的状态），而「仍开着」到底修饰谁，形式上判不出来。
// 去重后的长度是 1 ⇒ 用它判会漏掉这种形态（issue #411 的实际误判就是这么来的）。
func issueMentionsIn(text string) []int {
	prs := prRefsIn(text)
	var out []int
	for _, m := range numberRe.FindAllStringIndex(text, -1) {
		n, err := strconv.Atoi(text[m[0]+1 : m[1]])
		if err != nil || prs[n] {
			continue
		}
		out = append(out, n)
	}
	return out
}

// statusForLine 读一行散文/一个单元格的状态声明。
//
// **多个单号出现时一律不猜**（返回 StatusUnknown）：状态词只出现一次，而它
// 修饰哪一个单号形式上判不出来——猜一个就是替台账做决定。
//
// 这与 statusFromText 已有的「一行里两种标记同时出现 ⇒ 判不出」是同一条哲学：
// **归属不确定时宁可不表态**。假声明比没声明贵（它会被判据 1 拿去判出假矛盾、
// 被判据 5 拿去对账出假红——#411 就是后者：它其实早在 2026-11-17 就关了，
// 却因为「那条建议仍开着」这句被派生成待清偿）。
//
// 代价（如实登记）：多单号正文行的**辅助**状态声明不再生效 ⇒ 判据 1 对这类行
// 可能漏报矛盾。权威状态在**登记表的状态列**（statusFromCell，整行一个状态、
// 与单号个数无关），它不受本函数影响 ⇒漏掉的只是叙述性辅助，不是权威面。
func statusForLine(text string) Status {
	if len(issueMentionsIn(text)) > 1 {
		return StatusUnknown
	}
	return statusFromText(text)
}

// prRefsIn 找出文本里以 PR 形态出现的号。
//
// 口径：**PR 词之后、同一句读里的 #N 全是 PR 号**（句读以句号 / 分号 / 问号
// / 叹号断句）。
//
// 为什么不按「PR 词紧跟的下一个 #N」：台账里的写法是「合并 PR 分布在
// #339 / #340」，PR 词与号之间隔着中文词，按紧邻判会漏掉整串 PR 号——
// 漏判的后果是 PR 号冒充单号进视图（而 #339 这种号在别处也可能真是单号）。
//
// 为什么不按「整行」：一行里可能先说 PR、后面才提单号；按整行判会把那个
// 单号也吃成 PR 号，单号视图就少了一条。
func prRefsIn(text string) map[int]bool {
	out := map[int]bool{}
	runes := []rune(text)
	for i := 0; i+1 < len(runes); i++ {
		if !isPRWordAt(runes, i) {
			continue
		}
		end := clauseEnd(runes, i+2)
		// 偏移一律按字节算：正则给的是字节下标，混着 rune 下标用会在
		// 中文前缀后面切错位置（症状是「PR 号一个都没排除掉」）。
		sub := string(runes[i+2 : end])
		for _, m := range numberRe.FindAllStringIndex(sub, -1) {
			n, err := strconv.Atoi(sub[m[0]+1 : m[1]])
			if err != nil {
				continue
			}
			out[n] = true
		}
	}
	return out
}

// clauseEnd 从 start 起找句读终点（句号 / 分号 / 问号 / 叹号 / 换行）。
func clauseEnd(runes []rune, start int) int {
	for i := start; i < len(runes); i++ {
		switch runes[i] {
		case '。', '；', '？', '！', '.', ';', '?', '!', '\n':
			return i
		}
	}
	return len(runes)
}

// isPRWordAt 判断 i 处是不是独立的 PR 词。
func isPRWordAt(runes []rune, i int) bool {
	if !strings.EqualFold(string(runes[i]), string(prWord[0])) ||
		!strings.EqualFold(string(runes[i+1]), string(prWord[1])) {
		return false
	}
	if i > 0 && isWordRune(runes[i-1]) {
		return false
	}
	if i+2 < len(runes) && isWordRune(runes[i+2]) {
		return false
	}
	return true
}

// isWordRune 判断该字符是不是「词的一部分」（用于 PR 词的边界判定）。
func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// truncate 截断长文本（只用于展示）。
func truncate(s string, width int) string {
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	return string(r[:width]) + "…"
}
