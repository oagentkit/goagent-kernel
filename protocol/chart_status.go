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
// 词表，ChartStatus（归一判定，**已导出**——宿主拿不到归一能力就只能自己判
// 词形）与三个导出出口（AllChartStatuses / ChartStatusWords /
// AllChartStatusWords）全部读它。新增一个词或一档只需改这一处。
//
// # 与其它枚举出口的形状差异
//
// 这里的「合法值集合」是**分组**的：三档 × 若干词形，不是一张扁平词表。
// 故导出分四个：归一判定（ChartStatus）、档名集合、某档的词形、全部词形。
// protocol 的词表登记门禁（report_enum_test.go）为此单列一格「分组词表」登记面。
package protocol

import (
	"slices"
	"strings"
)

// chartWord* 图表状态的输入词形（归一输入词表的全部 43 个词形）。
//
// # 为什么每个词形都要有名字
//
// 词表取值一旦散落成**无名字字面量**，它与别的词表同形时就分不清是哪个概念——
// 「failed」既是会话终态（contract.StatusFailed），也是图表故障档的输入词形；
// 「running」同理。两者在代码里长一个样，于是「这个词属于哪张表」只能靠猜。
//
// 逐个命名之后，两个概念在代码里**显形区分**：本包的 chartWordFailed 与
// contract.StatusFailed 是两个不同的标识符——这正是
// harness-word-table-handcopy 门禁据以把「手抄」与「本包自有词表」分开的那一刀。
//
// # 为什么不是引用 contract 的常量
//
// 图表状态是**健康度**词表（ok/warn/down），contract 的是**生命周期**词表：
// 「running」在前者说的是「节点在跑」，在后者说的是「会话在跑」。
// 引用会把两个概念并成一个——那比手抄更糟：手抄至少是「两份各自独立的真相」，
// 引用则是「一份被强行合并的真相」。
const (
	// ok 档词形。
	chartWordOK        = "ok"
	chartWordHealthy   = "healthy"
	chartWordHealth    = "health"
	chartWordNormal    = "normal"
	chartWordUp        = "up"
	chartWordRunning   = "running"
	chartWordOnline    = "online"
	chartWordActive    = "active"
	chartWordGreen     = "green"
	chartWordPass      = "pass"
	chartWordGood      = "good"
	chartWordNormalCN  = "正常"
	chartWordHealthyCN = "健康"
	chartWordOnlineCN  = "在线"

	// warn 档词形。
	chartWordWarn       = "warn"
	chartWordWarning    = "warning"
	chartWordDegraded   = "degraded"
	chartWordPending    = "pending"
	chartWordYellow     = "yellow"
	chartWordSlow       = "slow"
	chartWordBusy       = "busy"
	chartWordWarnCN     = "警告"
	chartWordDegradedCN = "降级"
	chartWordSlowCN     = "缓慢"

	// down 档词形。
	chartWordDown        = "down"
	chartWordError       = "error"
	chartWordCritical    = "critical"
	chartWordCrit        = "crit"
	chartWordFailed      = "failed"
	chartWordFail        = "fail"
	chartWordCrash       = "crash"
	chartWordCrashed     = "crashed"
	chartWordOffline     = "offline"
	chartWordDead        = "dead"
	chartWordRed         = "red"
	chartWordUnreachable = "unreachable"
	chartWordStopped     = "stopped"
	chartWordDownCN      = "宕机"
	chartWordFaultCN     = "故障"
	chartWordAbnormalCN  = "异常"
	chartWordFailedCN    = "失败"
	chartWordStoppedCN   = "停止"
	chartWordOfflineCN   = "离线"
)

// 状态词白名单（实弹模型词形：healthy/down/error/正常/宕机…）。
var (
	chartStatusOK = map[string]bool{
		chartWordOK:        true,
		chartWordHealthy:   true,
		chartWordHealth:    true,
		chartWordNormal:    true,
		chartWordUp:        true,
		chartWordRunning:   true,
		chartWordOnline:    true,
		chartWordActive:    true,
		chartWordGreen:     true,
		chartWordPass:      true,
		chartWordGood:      true,
		chartWordNormalCN:  true,
		chartWordHealthyCN: true,
		chartWordOnlineCN:  true,
	}
	chartStatusWarn = map[string]bool{
		chartWordWarn:       true,
		chartWordWarning:    true,
		chartWordDegraded:   true,
		chartWordPending:    true,
		chartWordYellow:     true,
		chartWordSlow:       true,
		chartWordBusy:       true,
		chartWordWarnCN:     true,
		chartWordDegradedCN: true,
		chartWordSlowCN:     true,
	}
	chartStatusDown = map[string]bool{
		chartWordDown:        true,
		chartWordError:       true,
		chartWordCritical:    true,
		chartWordCrit:        true,
		chartWordFailed:      true,
		chartWordFail:        true,
		chartWordCrash:       true,
		chartWordCrashed:     true,
		chartWordOffline:     true,
		chartWordDead:        true,
		chartWordRed:         true,
		chartWordUnreachable: true,
		chartWordStopped:     true,
		chartWordDownCN:      true,
		chartWordFaultCN:     true,
		chartWordAbnormalCN:  true,
		chartWordFailedCN:    true,
		chartWordStoppedCN:   true,
		chartWordOfflineCN:   true,
	}
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

// ChartStatus 状态词归一为 ok|warn|down|空；未识别词保持空（前端按未知态渲染，不臆断）。
//
// # 为什么导出（第十轮）
//
// 图表状态词形是**外部喂进来的**（Prometheus / k8s / 各家看板的写法各不相同）：
// 归一表归 protocol 管，可**归一出口**必须让宿主够得着——
// 否则宿主只能自己判这个词形，于是同一个输入词在不同消费方得到不同结论，
// 而这正是判定词表登记门禁要防的漂移（只是发生在输入侧而不是判定侧）。
//
// 判定词表登记门禁（harness-word-table-normalizer-missing）要求：
// 输入归一词表必须声明一个**导出**的归一出口，且它读**同一张表**
// （chartStatusGroups）——归一到哪一档由那张表说了算。
func ChartStatus(s string) string {
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
