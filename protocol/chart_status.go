// chart_status.go —— 图表状态词的归一输入词表与导出枚举面。
//
// # 为什么必须可枚举
//
// 图表状态是**归一输入词表**：43 个输入词形（实弹模型词形：healthy / down /
// error / 正常 / 宕机…）分归 ok / warn / down 三档。宿主要渲染状态图例、要
// 校验外部喂进来的状态词，就必须知道「内核认哪些词、各归到哪档」——此前这些
// map 全是包私有且**零枚举出口**，宿主只能从 ChartNode.Status 的字段注释里
// 读到「ok|warn|down|空」三个档名，而内核实际认 43 个词形（含中文与大小写
// 变体）。宿主手抄的清单必然漂移：内核新增一个词，宿主那份不会跟着变，
// 且没有任何判据会红（issue #356）。
//
// # 单一事实源
//
// 档名与词形都只在本文件写一次：chartStatusGroups 是**唯一**的归一输入
// 词表，chartStatus（归一判定）与三个导出出口（AllChartStatuses /
// ChartStatusWords / AllChartStatusWords）全部读它。新增一个词或一档只需
// 改这一处。
//
// # 与其它枚举出口的形状差异
//
// 这里的「合法值集合」是**分组**的：三档 × 若干词形，不是一张扁平词表。
// 故导出分三个：档名集合、某档的词形、全部词形。protocol 的词表登记门禁
// （report_enum_test.go）为此单列一格「分组词表」登记面。
package protocol

import (
	"slices"
	"strings"
)

// 状态词白名单（实弹模型词形：healthy/down/error/正常/宕机…）。
var (
	chartStatusOK   = map[string]bool{"ok": true, "healthy": true, "health": true, "normal": true, "up": true, "running": true, "online": true, "active": true, "green": true, "pass": true, "good": true, "正常": true, "健康": true, "在线": true}
	chartStatusWarn = map[string]bool{"warn": true, "warning": true, "degraded": true, "pending": true, "yellow": true, "slow": true, "busy": true, "警告": true, "降级": true, "缓慢": true}
	chartStatusDown = map[string]bool{"down": true, "error": true, "critical": true, "crit": true, "failed": true, "fail": true, "crash": true, "crashed": true, "offline": true, "dead": true, "red": true, "unreachable": true, "stopped": true, "宕机": true, "故障": true, "异常": true, "失败": true, "停止": true, "离线": true}
)

// chartStatusGroups 图表状态的归一输入词表：档名 → 该档的输入词形。
//
// **单一事实源**：档名只在这里写一次，归一判定与三个导出出口全部读它。
// 三档的词形互不相交（实测逐词比对），故归一结果与遍历顺序无关。
var chartStatusGroups = []struct {
	name  string
	words map[string]bool
}{
	{name: "ok", words: chartStatusOK},
	{name: "warn", words: chartStatusWarn},
	{name: "down", words: chartStatusDown},
}

// chartStatus 状态词归一为 ok|warn|down|空；未识别词保持空（前端按未知态渲染，不臆断）。
func chartStatus(s string) string {
	t := strings.ToLower(strings.TrimSpace(s))
	if t == "" {
		return ""
	}
	for _, g := range chartStatusGroups {
		if g.words[t] {
			return g.name
		}
	}
	return ""
}

// AllChartStatuses 返回图表状态的归一目标档集合（排序后的**新切片**）。
//
// 宿主据此渲染状态图例 / 渲染器取档名，不必手抄「ok|warn|down」——
// 内核若新增一档，宿主那份不会跟着变，且没有任何判据会红（issue #356）。
func AllChartStatuses() []string {
	out := make([]string, 0, len(chartStatusGroups))
	for _, g := range chartStatusGroups {
		out = append(out, g.name)
	}
	slices.Sort(out)
	return out
}

// ChartStatusWords 返回归入该档的**输入词形**（排序后的新切片）。
//
// 档名未知时返回 nil（不臆断）。
func ChartStatusWords(bucket string) []string {
	for _, g := range chartStatusGroups {
		if g.name != bucket {
			continue
		}
		out := make([]string, 0, len(g.words))
		for w := range g.words {
			out = append(out, w)
		}
		slices.Sort(out)
		return out
	}
	return nil
}

// AllChartStatusWords 返回全部图表状态**输入词形**（43 个，排序后的新切片）。
//
// 这是各档词形的并集：宿主要「内核一共认哪些词」而不关心分档时用它。
func AllChartStatusWords() []string {
	var out []string
	for _, g := range chartStatusGroups {
		for w := range g.words {
			out = append(out, w)
		}
	}
	slices.Sort(out)
	return out
}
