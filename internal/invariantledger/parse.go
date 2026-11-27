// parse.go —— docs/INVARIANTS.md 两张登记表的解析（把声明面读成结构，不做判定）。
//
// 为什么解析 markdown 表而不是另立一份 YAML：登记面的读者是人——矩阵表要能
// 直接在文档里读，拆成「md 叙述 + yaml 清单」两份就必然漂移（漂移的方向恰好是
// 「文档里写着有场景、清单里其实没有」）。本仓已有同款先例：台账的单号状态
// （internal/ledgerstatus）就是解析 markdown 表得出的事实，判据拿它当唯一数据源。
//
// 解析口径（与 ledgerstatus 同一条纪律：解析器宽容、判据严格）：
//   - 表头按**列名**定位（不按列序），缺列 = 解析错误（给不出结论，退出码 2）；
//   - 单元格里的测试指针靠正则抽取——`dir/TestName` 形态，允许带反引号；
//   - 「—」「缺口」这类占位不是指针，抽取时自然落空，行级兜底由 Judge 判。
package invariantledger

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	// updatedRe 抓登记面版本戳行（文档顶部的 `updated: YYYY-MM-DD`）。
	updatedRe = regexp.MustCompile(`(?m)^updated:\s*(\S+)\s*$`)
	// testRefRe 抓测试指针：`dir/TestName`（dir 允许斜杠与点，Test 必须大写开头）。
	//
	// 只认 Test 大写开头：表里引用 Example/Fuzz 不在「必红判据」口径内
	// （评审要的是断言失败会红的判据）。
	testRefRe = regexp.MustCompile(`[A-Za-z0-9_][A-Za-z0-9_./-]*\.Test[A-Za-z0-9_]+`)
)

// 表头列名（按列名定位，与文档里的写法逐字一致）。
const (
	colID         = "id"
	colStatement  = "陈述"
	colMechanism  = "判定机制"
	colDocs       = "成文处"
	colJudgments  = "必红判据"
	colGap        = "缺口"
	colGapID      = "间隙"
	colWaiverPref = "豁免"
)

// ParseDoc 把登记面文档解析成 Registry。
//
// 解析错误（缺表 / 缺列 / 版本戳行缺失）返回 error：那是「给不出结论」
// （退出码 2），不是「判出违反」（退出码 1）——文档结构坏了与登记面判红
// 是两件事，混在一起会让读者往错的方向修。
func ParseDoc(body string) (*Registry, error) {
	m := updatedRe.FindStringSubmatch(body)
	if m == nil {
		return nil, fmt.Errorf("找不到版本戳行（文档顶部须有一行 `updated: YYYY-MM-DD`）")
	}
	reg := &Registry{Updated: m[1]}

	tables := parseTables(body)

	invTable, err := findTable(tables, colJudgments)
	if err != nil {
		return nil, fmt.Errorf("找不到不变量登记表（表头须含列 %q）：%w", colJudgments, err)
	}
	invCols := headerIndex(invTable.header)
	for _, need := range []string{colID, colStatement, colMechanism, colDocs, colJudgments, colGap} {
		if _, ok := invCols[need]; !ok {
			return nil, fmt.Errorf("不变量登记表缺列 %q（表头实得：%s）",
				need, strings.Join(invTable.header, " / "))
		}
	}
	for _, row := range invTable.rows {
		get := func(col string) string {
			i, ok := invCols[col]
			if !ok || i >= len(row) {
				return ""
			}
			return strings.TrimSpace(row[i])
		}
		reg.Invariants = append(reg.Invariants, InvariantRow{
			ID:        get(colID),
			Statement: get(colStatement),
			Mechanism: get(colMechanism),
			Docs:      get(colDocs),
			Judgments: extractRefs(get(colJudgments)),
			Gap:       get(colGap),
		})
	}

	matrix, err := findTable(tables, colGapID)
	if err != nil {
		return nil, fmt.Errorf("找不到插点矩阵（表头须含列 %q）：%w", colGapID, err)
	}
	if len(matrix.header) < 3 {
		return nil, fmt.Errorf("插点矩阵表头只有 %d 列：至少要有 间隙 + 一个故障类型列 + 豁免列", len(matrix.header))
	}
	waiverCol := -1
	for i, h := range matrix.header {
		if strings.Contains(h, colWaiverPref) {
			waiverCol = i
			break
		}
	}
	if waiverCol < 0 {
		return nil, fmt.Errorf("插点矩阵缺「豁免」列（表头实得：%s）——零场景的行必须有书面豁免落点",
			strings.Join(matrix.header, " / "))
	}
	for _, row := range matrix.rows {
		if len(row) == 0 {
			continue
		}
		// 间隙 id 取首列第一个词（首列形如「G1 创建→领取」，标签供人读）。
		id := strings.Fields(row[0])
		g := GapRow{}
		if len(id) > 0 {
			g.ID = id[0]
		}
		// 故障类型格 = 首列之后、豁免列之前的所有列（行比表头短时截到行尾）。
		end := waiverCol
		if end > len(row) {
			end = len(row)
		}
		if end > 1 {
			for _, cell := range row[1:end] {
				g.Evidence = append(g.Evidence, extractRefs(cell)...)
			}
		}
		if waiverCol < len(row) {
			g.Waiver = strings.TrimSpace(row[waiverCol])
		}
		reg.Gaps = append(reg.Gaps, g)
	}
	return reg, nil
}

// table 一张 markdown 表。
type table struct {
	header []string
	rows   [][]string
}

// parseTables 抽出文档里全部带表头的 markdown 表（连续 `|` 行块，第二行是分隔行）。
func parseTables(body string) []table {
	var out []table
	lines := strings.Split(body, "\n")
	for i := 0; i+1 < len(lines); i++ {
		h := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(h, "|") {
			continue
		}
		sep := strings.TrimSpace(lines[i+1])
		if !isSeparator(sep) {
			continue
		}
		t := table{header: splitRow(h)}
		i += 2
		for ; i < len(lines); i++ {
			line := strings.TrimSpace(lines[i])
			if !strings.HasPrefix(line, "|") {
				break
			}
			t.rows = append(t.rows, splitRow(line))
		}
		out = append(out, t)
		// for 的 i++ 会再加一——表块消费到块尾，回退一位让外层继续。
		i--
	}
	return out
}

// findTable 按「表头含指定列名」定位唯一一张表。
func findTable(tables []table, markerCol string) (*table, error) {
	var hit *table
	for i := range tables {
		for _, h := range tables[i].header {
			if strings.TrimSpace(h) == markerCol {
				if hit != nil {
					return nil, fmt.Errorf("表头含 %q 的表出现两次：登记面只能有一张", markerCol)
				}
				hit = &tables[i]
				break
			}
		}
	}
	if hit == nil {
		return nil, fmt.Errorf("一张都没有")
	}
	return hit, nil
}

// headerIndex 表头列名 → 列下标（列名去空格）。
func headerIndex(header []string) map[string]int {
	out := make(map[string]int, len(header))
	for i, h := range header {
		out[strings.TrimSpace(h)] = i
	}
	return out
}

// isSeparator 判 markdown 表分隔行（|---|:---:| 形态）。
func isSeparator(line string) bool {
	if !strings.HasPrefix(line, "|") {
		return false
	}
	cells := splitRow(line)
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		c = strings.TrimSpace(c)
		c = strings.Trim(c, ":")
		if c == "" || strings.Trim(c, "-") != "" {
			return false
		}
	}
	return true
}

// splitRow 按 `|` 切一行（去掉首尾空段）。
func splitRow(line string) []string {
	cells := strings.Split(line, "|")
	// 首段与尾段在 `| a | b |` 形态下是空串，剥掉。
	if len(cells) > 0 && strings.TrimSpace(cells[0]) == "" {
		cells = cells[1:]
	}
	if len(cells) > 0 && strings.TrimSpace(cells[len(cells)-1]) == "" {
		cells = cells[:len(cells)-1]
	}
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}

// extractRefs 从单元格文本里抽全部测试指针（去重、保序）。
func extractRefs(cell string) []string {
	seen := map[string]bool{}
	var out []string
	for _, ref := range testRefRe.FindAllString(cell, -1) {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		out = append(out, ref)
	}
	return out
}
