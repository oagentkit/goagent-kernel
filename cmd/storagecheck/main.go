// storagecheck —— 存储选型判据（issue #424）。
//
// 判「场景选的存储对不对」：在允许集内 + 提供该场景必需的全部能力 +
// 能力断言在代码里找得到落点。
//
// 与 cmd/deploymatrix 分开：deploymatrix 判「能力齐不齐」（红在缺能力），
// 本判据红在「选错引擎」——两件事的失败形态不同，混在一起会让「哪个门禁
// 在管我」变得不可追。
package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// run 判据的 CLI 装配（返回退出码——便于用例直接断言，不靠子进程）。
//
// 输出里必须**看得见口径**：选型口径若只躺在声明面里没人看得见，它就只是一段
// 没人验证的散文 ⇒ 口径正文、逐条要点、已知不对称的摘要都打出来。
func run(args []string, stdout, stderr io.Writer) int {
	root := "."
	if len(args) > 0 {
		root = args[0]
	}
	probs, notes, err := check(root)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "storagecheck: %v\n", err)
		return 1
	}
	for _, n := range notes {
		_, _ = fmt.Fprintf(stdout, "storagecheck: %s\n", n)
	}
	if len(probs) > 0 {
		for _, p := range probs {
			_, _ = fmt.Fprintf(stderr, "storagecheck: %s\n", p)
		}
		return 1
	}
	// 成功文案逐条列出本门禁现在判什么——口径要看得见。
	//
	// 为什么逐条列：门禁绿了，读的人得知道「绿」到底盖住了什么。一句
	// 「选型没问题」把 kind 值域封闭、写者数交叉校验、门面对位这些判据
	// 全吞掉 ⇒ 后来人以为门禁只管场景选型，改错地方也不知道。
	//
	// ⚠ 门面那几条**按门面数条件化**：声明面里一扇门面都没有时，F1–F4 **没有
	// 判定面**（判据无从下手）⇒ 此时宣称「判过」就是谎报 ⇒ 如实说「未判定」。
	// ⚠ 这**不是**「零门面即红」——把门禁形状硬编码成「必须两扇门」是逼人编门
	// （且会打红既有合成夹具）⇒ 「零门面即红」留作后续议题。
	base := "storagecheck ✓（场景选型在允许集内 + 能力齐备 + 落点可解析 + " +
		"存储 kind 值域封闭 + 写者数与写者上限交叉校验"
	if facadeCount(notes) == 0 {
		_, _ = fmt.Fprintln(stdout, base+"）；门面集合相关判据本门禁**未判定**"+
			"（声明面里没有任何门面声明 ⇒ F1 派生集逐字相符 / F2 清单形状 / F3 两侧同款 / "+
			"F4 面访问器落点 四项均无判定面）")
		return 0
	}
	_, _ = fmt.Fprintln(stdout, base+" + 门面清单形状合法（F2：面访问器非空 / 不重复 / 与其它导出方法不重叠）+ "+
		"两侧门面的面访问器集合相等（F3）+ 门面的导出方法集合与代码派生集逐字相符（F1）+ "+
		"每个面访问器都有能力落点或已登记（F4）+ 有方法级落点的存储必须登记门面段（F5））")
	return 0
}

// facadeCountPrefix 门面判定面条目的固定前缀（facade.go 打，main.go 读）。
const facadeCountPrefix = "说明 门面判定面："

// facadeCount 从门禁说明里取**门面数**（取不到 ⇒ 0）。
//
// 为什么靠说明行传递：check() 的签名被既有用例钉住（不能改）⇒ 门面数只能从
// 说明行回传。为什么取不到按 0 算：文案条件化是**少说**的方向——拿不准时只能说
// 「没判定」，不能宣称判过 ⇒ 解析失败走保守侧。
func facadeCount(notes []string) int {
	for _, n := range notes {
		if !strings.HasPrefix(n, facadeCountPrefix) {
			continue
		}
		num, _, _ := strings.Cut(strings.TrimPrefix(n, facadeCountPrefix), " ")
		v, err := strconv.Atoi(num)
		if err != nil {
			return 0
		}
		return v
	}
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
