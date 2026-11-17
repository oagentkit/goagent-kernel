// usage.go —— USAGE 机器口径块与清单的逐行比对（issue #277）。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// checkUsageBlock 校验 USAGE 的机器口径块与清单逐行一致。
func checkUsageBlock(root string, m *Matrix) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(root, usagePath))
	if err != nil {
		return nil, fmt.Errorf("读 %s 失败（deploy-matrix 块是这道门禁的判据面）: %w", usagePath, err)
	}
	rows, err := parseUsageRows(string(b))
	if err != nil {
		return []string{usagePath + ": " + err.Error()}, nil
	}
	var probs []string
	for _, s := range m.Scenarios {
		row, ok := rows[s.ID]
		if !ok {
			probs = append(probs, fmt.Sprintf("%s: deploy-matrix 块漏登记场景 %q", usagePath, s.ID))
			continue
		}
		if row.storage != s.Storage {
			probs = append(probs, fmt.Sprintf("%s: 场景 %q 的推荐存储写 %q，清单是 %q",
				usagePath, s.ID, row.storage, s.Storage))
		}
		if row.worker != s.Worker {
			probs = append(probs, fmt.Sprintf("%s: 场景 %q 的 Worker 模式写 %q，清单是 %q",
				usagePath, s.ID, row.worker, s.Worker))
		}
		if row.readiness != s.Readiness {
			probs = append(probs, fmt.Sprintf("%s: 场景 %q 的当前状态写 %q，清单是 %q",
				usagePath, s.ID, row.readiness, s.Readiness))
		}
		delete(rows, s.ID)
	}
	for id := range rows {
		probs = append(probs, fmt.Sprintf("%s: deploy-matrix 块登记了清单里不存在的场景 %q", usagePath, id))
	}
	return probs, nil
}

// usageRow USAGE 机器口径块里的一行。
type usageRow struct {
	storage   string
	worker    string
	readiness string
}

// parseUsageRows 解出 `<!-- deploy-matrix:begin -->` 与
// `<!-- deploy-matrix:end -->` 之间的场景行。
func parseUsageRows(text string) (map[string]usageRow, error) {
	const begin = "<!-- deploy-matrix:begin -->"
	const end = "<!-- deploy-matrix:end -->"
	i := strings.Index(text, begin)
	if i < 0 {
		return nil, fmt.Errorf("找不到 %s（机器口径块被删 = 判据静默失效）", begin)
	}
	j := strings.Index(text[i:], end)
	if j < 0 {
		return nil, fmt.Errorf("找不到 %s", end)
	}
	block := text[i+len(begin) : i+j]
	out := map[string]usageRow{}
	for _, line := range strings.Split(block, "\n") {
		m := usageRowRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		id := m[1]
		if _, dup := out[id]; dup {
			return nil, fmt.Errorf("场景 %q 在块里重复", id)
		}
		out[id] = usageRow{storage: m[2], worker: m[3], readiness: m[4]}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("块里没有任何场景行（表结构变了？）")
	}
	return out, nil
}
