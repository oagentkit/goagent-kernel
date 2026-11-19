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
		fmt.Fprintf(stderr, "storagecheck: %v\n", err)
		return 1
	}
	for _, n := range notes {
		fmt.Fprintf(stdout, "storagecheck: %s\n", n)
	}
	if len(probs) > 0 {
		for _, p := range probs {
			fmt.Fprintf(stderr, "storagecheck: %s\n", p)
		}
		return 1
	}
	fmt.Fprintln(stdout, "storagecheck ✓（场景选型在允许集内 + 能力齐备 + 落点可解析 + 写者数不越界）")
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
