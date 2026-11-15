// thresholds.go —— 结构债阈值的执行体（超长文件 / god object）。
//
// 从 archcheck.go 拆出：Run 只保留「调一次、拿结论」的流程，阈值判定的
// 错误语义（遍历/解析失败 ⇒ 给不出结论）与报告文案集中在这里。
package archcheck

import (
	"fmt"
	"strings"
)

// thresholdFindings 执行结构阈值判据，返回报告用发现（空 = 无超限）。
//
// 任一条判据给不出结论都返回 error：超长文件遍历失败或 god object 解析失败
// 意味着那棵树没被判定——「没判」不能显示成「通过」。
func thresholdFindings(root string, th Threshold) ([]string, error) {
	var out []string
	if th.MaxFileLines > 0 {
		long, err := longFilesReport(root, th.ScanRoots, th.MaxFileLines)
		if err != nil {
			return nil, err
		}
		if len(long) > 0 {
			out = append(out, fmt.Sprintf("超长文件（阈值 %d 行）%d 个:\n    %s",
				th.MaxFileLines, len(long), strings.Join(long, "\n    ")))
		}
	}
	if th.MaxMethodsPerType > 0 {
		gods, err := godObjects(root, th.ScanRoots, th.MaxMethodsPerType, th.forwardingShellLimit())
		if err != nil {
			return nil, err
		}
		if len(gods) > 0 {
			out = append(out, fmt.Sprintf("god object（阈值 %d 实质方法/类型）%d 个:\n    %s",
				th.MaxMethodsPerType, len(gods), strings.Join(gods, "\n    ")))
		}
	}
	return out, nil
}
