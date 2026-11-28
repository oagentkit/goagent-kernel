// parse.go —— docs/DELIVERY-SEMANTICS.md 两张表的解析（把声明面读成结构，不做判定）。
//
// 与 internal/invariantledger、internal/hostsurface 的 md 表解析同款口径：
// 解析器宽容、判据严格；表头按**逐字表头**定位，缺表/缺列 = 解析错误（退出码 2，
// 「给不出结论」与「判出违反」是两件事）。
package main

import (
	"fmt"
	"strings"
)

// parseDoc 把交付语义登记面解析成 Registry。
func parseDoc(body string) (*Registry, error) {
	m := updatedRe.FindStringSubmatch(body)
	if m == nil {
		return nil, fmt.Errorf("找不到版本戳行（顶部须有一行 `updated: YYYY-MM-DD`）")
	}
	reg := &Registry{Updated: m[1]}
	lines := strings.Split(body, "\n")

	capIdx := findHeader(lines, capTableHeader)
	if capIdx < 0 {
		return nil, fmt.Errorf("找不到能力面表（表头须逐字为：%s）", capTableHeader)
	}
	if capIdx+1 >= len(lines) || !strings.HasPrefix(strings.TrimSpace(lines[capIdx+1]), "|---") {
		return nil, fmt.Errorf("能力面表头之后缺分隔行（|---| 形态）")
	}
	for i := capIdx + 2; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "|") {
			break
		}
		cells := splitRow(line)
		if len(cells) < 5 {
			return nil, fmt.Errorf("能力面表行只有 %d 列（须 5 列），行内容：%s", len(cells), line)
		}
		reg.Rows = append(reg.Rows, CapRow{
			Face: cells[0], Pkgs: cells[1], Tier: cells[2], Mechanism: cells[3], Judgments: cells[4],
		})
	}
	if len(reg.Rows) == 0 {
		return nil, fmt.Errorf("能力面表一行都没有（表头下没有数据行）")
	}

	wlIdx := findHeader(lines, wlTableHeader)
	if wlIdx < 0 {
		return nil, fmt.Errorf("找不到失真声明白名单表（表头须逐字为：%s）", wlTableHeader)
	}
	if wlIdx+1 >= len(lines) || !strings.HasPrefix(strings.TrimSpace(lines[wlIdx+1]), "|---") {
		return nil, fmt.Errorf("白名单表头之后缺分隔行（|---| 形态）")
	}
	for i := wlIdx + 2; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "|") {
			break
		}
		cells := splitRow(line)
		if len(cells) < 2 {
			return nil, fmt.Errorf("白名单表行只有 %d 列（须 2 列），行内容：%s", len(cells), line)
		}
		reg.Whitelist = append(reg.Whitelist, WLRow{
			File:   strings.Trim(strings.TrimSpace(cells[0]), "`"),
			Reason: strings.Trim(strings.TrimSpace(cells[1]), "`"),
		})
	}
	if len(reg.Whitelist) == 0 {
		return nil, fmt.Errorf("白名单表一行都没有（拒绝语境的家要有登记，否则扫描面无从划界）")
	}
	return reg, nil
}

// findHeader 找逐字匹配的表头行（返回下标，-1 = 没有）。
func findHeader(lines []string, header string) int {
	for i, l := range lines {
		if strings.TrimSpace(l) == header {
			return i
		}
	}
	return -1
}

// splitRow 按 `|` 切一行（剥首尾空段）。
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
