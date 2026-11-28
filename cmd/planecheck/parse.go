// parse.go —— docs/PLANES.md 两张表的解析（把声明面读成结构，不做判定）。
//
// 与 invariantledger/hostsurface/semanticscheck 的 md 表解析同款口径：解析器
// 宽容、判据严格；表头逐字定位，缺表/缺列 = 解析错误（退出码 2）。
package main

import (
	"fmt"
	"strings"
)

// parseDoc 把三平面登记面解析成 Registry。
func parseDoc(body string) (*Registry, error) {
	m := updatedRe.FindStringSubmatch(body)
	if m == nil {
		return nil, fmt.Errorf("找不到版本戳行（顶部须有一行 `updated: YYYY-MM-DD`）")
	}
	reg := &Registry{Updated: m[1]}
	lines := strings.Split(body, "\n")

	mapIdx := findHeader(lines, mapTableHeader)
	if mapIdx < 0 {
		return nil, fmt.Errorf("找不到包→平面映射表（表头须逐字为：%s）", mapTableHeader)
	}
	if mapIdx+1 >= len(lines) || !strings.HasPrefix(strings.TrimSpace(lines[mapIdx+1]), "|---") {
		return nil, fmt.Errorf("映射表头之后缺分隔行（|---| 形态）")
	}
	for i := mapIdx + 2; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "|") {
			break
		}
		cells := splitRow(line)
		if len(cells) < 3 {
			return nil, fmt.Errorf("映射表行只有 %d 列（须 3 列），行内容：%s", len(cells), line)
		}
		reg.Mapping = append(reg.Mapping, MapRow{
			Pkg:   strings.Trim(cells[0], "`"),
			Plane: strings.TrimSpace(cells[1]),
			Duty:  cells[2],
		})
	}
	if len(reg.Mapping) == 0 {
		return nil, fmt.Errorf("映射表一行都没有（表头下没有数据行）")
	}

	ruleIdx := findHeader(lines, ruleTableHeader)
	if ruleIdx < 0 {
		return nil, fmt.Errorf("找不到跨面规则表（表头须逐字为：%s）", ruleTableHeader)
	}
	if ruleIdx+1 >= len(lines) || !strings.HasPrefix(strings.TrimSpace(lines[ruleIdx+1]), "|---") {
		return nil, fmt.Errorf("规则表头之后缺分隔行（|---| 形态）")
	}
	for i := ruleIdx + 2; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "|") {
			break
		}
		cells := splitRow(line)
		if len(cells) < 4 {
			return nil, fmt.Errorf("规则表行只有 %d 列（须 4 列），行内容：%s", len(cells), line)
		}
		reg.Rules = append(reg.Rules, RuleRow{
			From: strings.TrimSpace(cells[0]), To: strings.TrimSpace(cells[1]),
			Verdict: strings.TrimSpace(cells[2]), Reason: cells[3],
		})
	}
	if len(reg.Rules) == 0 {
		return nil, fmt.Errorf("跨面规则表一行都没有（没有规则表，跨面依赖退回靠 review 感觉）")
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
