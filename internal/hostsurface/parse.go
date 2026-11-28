// parse.go —— docs/HOST-SURFACE.md 两张登记表的解析（把声明面读成结构，不做判定）。
//
// 与 internal/invariantledger.ParseDoc 同款口径：解析器宽容、判据严格；表头按
// 列名定位（不按列序），缺列 = 解析错误（给不出结论，退出码 2）；markdown 表
// 是单源——拆成「md 叙述 + yaml 清单」两份必然漂移（漂移方向恰是「文档里写着
// 分类了、清单里其实没有」）。先例：台账状态（ledgerstatus）与不变量登记面
// （invariantledger）都是解析 markdown 表得出的事实。
package hostsurface

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	// updatedRe 抓登记面版本戳行（文档顶部的 `updated: YYYY-MM-DD`）。
	updatedRe = regexp.MustCompile(`(?m)^updated:\s*(\S+)\s*$`)
)

// 表头列名（按列名定位，与文档里的写法逐字一致）。
const (
	colStep   = "步骤"
	colHost   = "宿主提供"
	colKernel = "kernel 兑现"
	colPkg    = "pkg"
	colClass  = "分类"
	colDuty   = "职责"
)

// ParseDoc 把接入面登记面文档解析成 Registry。
//
// 解析错误（缺表 / 缺列 / 版本戳行缺失）返回 error：那是「给不出结论」
// （退出码 2），不是「判出违反」（退出码 1）。
func ParseDoc(body string) (*Registry, error) {
	m := updatedRe.FindStringSubmatch(body)
	if m == nil {
		return nil, fmt.Errorf("找不到版本戳行（文档顶部须有一行 `updated: YYYY-MM-DD`）")
	}
	reg := &Registry{Updated: m[1]}
	tables := parseTables(body)

	stepTable, err := findTable(tables, colStep)
	if err != nil {
		return nil, fmt.Errorf("找不到主链对照表（表头须含列 %q）：%w", colStep, err)
	}
	stepCols := headerIndex(stepTable.header)
	for _, need := range []string{colStep, colHost, colKernel} {
		if _, ok := stepCols[need]; !ok {
			return nil, fmt.Errorf("主链对照表缺列 %q（表头实得：%s）",
				need, strings.Join(stepTable.header, " / "))
		}
	}
	for _, row := range stepTable.rows {
		get := func(col string) string {
			i, ok := stepCols[col]
			if !ok || i >= len(row) {
				return ""
			}
			return strings.TrimSpace(row[i])
		}
		raw := get(colStep)
		id := ""
		if fields := strings.Fields(raw); len(fields) > 0 {
			id = fields[0]
		}
		reg.Steps = append(reg.Steps, StepRow{
			ID:             id,
			Name:           strings.TrimSpace(strings.TrimPrefix(raw, id)),
			HostProvides:   get(colHost),
			KernelDelivers: get(colKernel),
		})
	}

	pkgTable, err := findTable(tables, colClass)
	if err != nil {
		return nil, fmt.Errorf("找不到包分类表（表头须含列 %q）：%w", colClass, err)
	}
	pkgCols := headerIndex(pkgTable.header)
	for _, need := range []string{colPkg, colClass, colDuty} {
		if _, ok := pkgCols[need]; !ok {
			return nil, fmt.Errorf("包分类表缺列 %q（表头实得：%s）",
				need, strings.Join(pkgTable.header, " / "))
		}
	}
	for _, row := range pkgTable.rows {
		get := func(col string) string {
			i, ok := pkgCols[col]
			if !ok || i >= len(row) {
				return ""
			}
			return strings.TrimSpace(row[i])
		}
		reg.Packages = append(reg.Packages, PackageRow{
			Pkg:   get(colPkg),
			Class: get(colClass),
			Duty:  get(colDuty),
		})
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
		i-- // 块尾行回退一位，让外层继续扫
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
