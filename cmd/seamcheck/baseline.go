// baseline.go —— 基线比较、棘轮与读写（issue #400）。
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/oekit/pkg/errors"
)

// compare 当前结果 vs 基线：集合逐条相等 + 每接缝约束只增 + 总数只升。
func compare(base, cur Report) error {
	baseByID, err := indexSeams(base)
	if err != nil {
		return fmt.Errorf("基线自身不合法: %w", err)
	}
	curByID, err := indexSeams(cur)
	if err != nil {
		return fmt.Errorf("当前扫描自身不合法: %w", err)
	}

	var problems []string
	for id := range baseByID {
		if _, ok := curByID[id]; !ok {
			problems = append(problems, fmt.Sprintf("接缝 %s 从当前扫描中消失：登记的清单缺一即红（确已删除请 -update -force）", id))
		}
	}
	for id := range curByID {
		if _, ok := baseByID[id]; !ok {
			problems = append(problems, fmt.Sprintf("接缝 %s 未登记：新增接缝须 -update 入基线并评审", id))
		}
	}
	for id, cs := range curByID {
		bs, ok := baseByID[id]
		if !ok {
			continue
		}
		for _, w := range bs.Written {
			if !slices.Contains(cs.Written, w) {
				problems = append(problems, fmt.Sprintf("接缝 %s 的约束 %q 成文回退：棘轮只允许上升", id, w))
			}
		}
	}
	if cur.FullyWritten < base.FullyWritten {
		problems = append(problems, fmt.Sprintf("三条约束写齐的接缝数从 %d 降到 %d：棘轮只允许上升", base.FullyWritten, cur.FullyWritten))
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

// ratchetOnlyUp 供 -update 复用：新基线不得删接缝、不得减少已写约束。
func ratchetOnlyUp(old, cur Report) error {
	oldByID, err := indexSeams(old)
	if err != nil {
		return err
	}
	curByID, err := indexSeams(cur)
	if err != nil {
		return err
	}
	var problems []string
	for id, os := range oldByID {
		cs, ok := curByID[id]
		if !ok {
			problems = append(problems, fmt.Sprintf("接缝 %s 消失", id))
			continue
		}
		for _, w := range os.Written {
			if !slices.Contains(cs.Written, w) {
				problems = append(problems, fmt.Sprintf("接缝 %s 的约束 %q 消失", id, w))
			}
		}
	}
	if cur.FullyWritten < old.FullyWritten {
		problems = append(problems, fmt.Sprintf("写齐数从 %d 降到 %d", old.FullyWritten, cur.FullyWritten))
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

func indexSeams(r Report) (map[string]Seam, error) {
	out := make(map[string]Seam, len(r.Seams))
	for _, s := range r.Seams {
		if s.ID == "" {
			return nil, errors.New("接缝 ID 为空")
		}
		if _, dup := out[s.ID]; dup {
			return nil, fmt.Errorf("接缝 %s 重复登记", s.ID)
		}
		out[s.ID] = s
	}
	return out, nil
}

func loadBaseline(path string) (Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Report{}, err
	}
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return Report{}, fmt.Errorf("%s: %w", path, err)
	}
	if r.Schema != 1 {
		return Report{}, fmt.Errorf("%s: 未知 schema %d", path, r.Schema)
	}
	return r, nil
}

func writeBaseline(path string, r Report) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// printReport 人类可读的三行摘要（数字与基线并列，便于对照）。
func printReport(w io.Writer, cur Report, base *Report) {
	reportf(w, "seamcheck: 接缝声明 %d / 调用点 %d / 三条约束写齐 %d\n",
		cur.Declarations, cur.Callsites, cur.FullyWritten)
	reportf(w, "  约束覆盖：不得重入 %d / 不得阻塞 %d / panic 隔离 %d\n",
		countWritten(cur, constraintReentry), countWritten(cur, constraintBlocking), countWritten(cur, constraintPanic))
	if base != nil {
		reportf(w, "  基线：声明 %d / 调用点 %d / 写齐 %d\n", base.Declarations, base.Callsites, base.FullyWritten)
		if delta := cur.Callsites - base.Callsites; delta != 0 {
			reportf(w, "  调用点变化 %+d（基线是快照；确认无遗漏后用 -update 刷新）\n", delta)
		}
	}
}

// reportf 往 w 写报告并显式丢弃写入错误（报告面就是最终输出，写不出去没有
// 可做的事；与 cmd/pkgcov 同一条口径，避免 errcheck 的静默豁免）。
func reportf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func countWritten(r Report, constraint string) int {
	n := 0
	for _, s := range r.Seams {
		if slices.Contains(s.Written, constraint) {
			n++
		}
	}
	return n
}
