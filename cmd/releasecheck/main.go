// Command releasecheck —— 发版纪律的机器判据（把散文变成门禁）。
//
// 四条纪律此前只写在 AGENTS.md「发版纪律」与 docs/AI-GUIDE.md §3.6 里，
// 全靠人手工核对。它们各自对应一种**只有下游才会发现**的残缺：
//
//  1. **tag 快照里的 CHANGELOG 必须已含该 tag 自己那一段**。顺序反了（先打 tag
//     后生成）⇒ 下游 go mod download 拿到的模块里，发布说明缺自己那版。
//  2. **CHANGELOG 不得含 U+FFFD**。源 commit message 带坏字节时生成器会忠实
//     复现，而坏字节在两侧都读不出原因。
//  3. **一个提交只装一类内容**。「依赖文件 + 非依赖文件」混装会让行为变更落进
//     「依赖地板」段——下游扫 breaking 时根本看不见（2026-10-25 v0.13.0 实例）。
//  4. **tag 必须是 annotated**。轻量 tag 承载不了发布说明，只剩一个指向。
//
// # 判定面：只在 tag 快照上判
//
// HEAD 没有 exact tag（普通提交 / PR 分支）⇒ **显式跳过并 exit 0**。故它可以
// 安全地挂在 make check 里：日常绿，打 tag 后 CI 的 tag 运行必然有 exact tag，
// 那时它才是硬判据。这条与「跳过必须显式、不许静默」一致——跳过时会打印原因。
//
// # 为什么第 3 条带一张闭合的 legacy 清单
//
// 已发布的提交不可重写（改写公开历史会让下游按 commit 引用的校验失效）。故
// 历史上已发生的混装只能登记、不能修；清单**只减不增**，新增混装一律报红。
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/oagentkit/runtime/procx"
)

const changelogPath = "CHANGELOG.md"

// legacyMixed 已发布历史里已知的混装提交（**闭合清单，只减不增**）。
//
// 判据不变：新提交一旦混装就红。这张表存在只因为「已发布的历史不可重写」——
// 不登记它们，本门禁在 v0.41.0 上会永远红，而红理由是既成事实、修不了。
var legacyMixed = map[string]string{
	"b2e6305": "v0.70.0：go get oagentkit/toolname 的 go.mod/go.sum 与 README 依赖列同步混装；" +
		"main 已推送不可重写。依赖部分是 v0.70.0 改指的唯一依赖入口，与文档同步同提交",
	"6ad6650": "v0.70.0：toolname 改指 oagentkit 的提交混装 go.mod/go.sum 与 import 重指；" +
		"main 已推送不可重写。其依赖部分是新 module oagentkit/toolname 的引入，与" +
		"import 重指互为因果；行为变更已在 v0.70.0 CHANGELOG 写明，未藏进依赖段",
	"de275a2": "v0.69.0：toolname 上移提交的 go.mod/go.sum 与删包/import 重指混装；" +
		"PR #472 已合并发布不可重写。其依赖部分是 oekit/pkg v0.57→v0.58 的地板抬升，" +
		"与被删包的替代 import 互为因果；行为变更已在本段与 CHANGELOG 写明，未藏进依赖段",
	"02a5e39": "v0.67.0：参考实现迁移主提交的 go.mod/go.sum 与删包文件混装；" +
		"PR #470 已合并发布不可重写。其依赖部分是 pgx/modernc 驱动撤除，与被删的" +
		"两个参考实现互为因果（不 tidy 删不干净）；行为变更已在本段与 CHANGELOG" +
		"写明（examples 两目录删除，消费方改指 runtime 仓），未藏进依赖段",
	"00b4d1d": "v0.66.0：pii/subhub 上移提交的 go.mod/go.sum 与删包/import 重指混装；" +
		"PR #464 已合并发布不可重写。其依赖部分是 oekit/pkg v0.52 到 v0.57 的地板抬升，" +
		"与被删包的替代 import 互为因果（不抬版本编译不过）；行为变更已在本段与" +
		"CHANGELOG 写明（pii Stable 退役），未藏进依赖段",
	"7d8d1af": "v0.41.0：chore(deps) 混装了 backend 测试改动；main 已推送不可重写，" +
		"其非依赖部分是纯测试（下游零动作），行为变更没有借此藏进依赖段",
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "releasecheck: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	version, ok, err := exactTag()
	if err != nil {
		return err
	}
	if !ok {
		fmt.Println("releasecheck: HEAD 无 exact tag（非发版提交）⇒ 跳过——本门禁只判 tag 快照")
		return nil
	}

	data, err := os.ReadFile(changelogPath)
	if err != nil {
		return fmt.Errorf("读 %s 失败: %w", changelogPath, err)
	}
	md := string(data)

	var problems []string

	// 4. annotated tag
	kind, err := gitOut("cat-file", "-t", version)
	if err != nil {
		return err
	}
	if kind != "tag" {
		problems = append(problems, fmt.Sprintf(
			"tag %s 是 %s（轻量 tag）——轻量 tag 承载不了发布说明，且 go get 的人未必看 tag 注解；用 git tag -a 重打", version, kind))
	}

	// 1. CHANGELOG 含本版段
	if !hasSection(md, version) {
		problems = append(problems, fmt.Sprintf(
			"%s 缺本版段「## [%s]」——tag 快照就是下游拿到的模块内容，先打 tag 后生成会让那一版的发布说明残缺",
			changelogPath, version))
	}

	// 2. 编码损坏
	if line, bad := firstReplacementChar(md); bad {
		problems = append(problems, fmt.Sprintf(
			"%s 第 %d 行含 U+FFFD（替换字符）——源 commit message 里的坏字节被生成器忠实复现了，呈现层修一次并记下来源",
			changelogPath, line))
	}

	// 3. 区间内混装提交
	prev, hasPrev, err := previousTag(version)
	if err != nil {
		return err
	}
	if hasPrev {
		raw, err := gitOut("log", "--format=%x01%H", "--name-only", prev+".."+version)
		if err != nil {
			return err
		}
		for _, v := range mixViolations(parseLog(raw), legacyMixed) {
			problems = append(problems, v.String())
		}
	} else {
		fmt.Printf("releasecheck: 找不到 %s 的前一个 tag ⇒ 跳过混装提交判定（首版场景）\n", version)
	}

	if len(problems) > 0 {
		fmt.Printf("releasecheck: %s 违反发版纪律 %d 处：\n", version, len(problems))
		for _, p := range problems {
			fmt.Println("  - " + p)
		}
		fmt.Println("口径与处置见 docs/AI-GUIDE.md §3.6「任务 F：发版」")
		return fmt.Errorf("发版纪律未满足")
	}

	fmt.Printf("releasecheck: %s 发版纪律 ✓（annotated tag / CHANGELOG 含本版段 / 无编码损坏 / 区间内无混装提交）\n", version)
	return nil
}

// exactTag 返回 HEAD 上的 exact tag；没有则 ok=false（不是错误）。
func exactTag() (string, bool, error) {
	out, err := gitOut("describe", "--exact-match", "--tags", "HEAD")
	if err != nil {
		// describe 在没有 exact match 时以非零退出——这是「不是发版提交」，
		// 不是故障。用 stderr 区分真故障与「没匹配上」成本高，故只按
		// 「输出为空=没有 tag」判：有输出却报错才是真故障。
		if out == "" {
			return "", false, nil
		}
		return "", false, err
	}
	return out, true, nil
}

// previousTag 返回给定 tag 之前最近的一个 tag（按 semver 排序不参与——用
// git 的默认可达序，与生成器切段口径一致）。
func previousTag(version string) (string, bool, error) {
	out, err := gitOut("describe", "--tags", "--abbrev=0", version+"^")
	if err != nil {
		if out == "" {
			return "", false, nil
		}
		return "", false, err
	}
	return out, true, nil
}

// gitOut 跑一条 git 命令并返回 trim 过的 stdout（失败时也返回 stdout，
// 供调用方区分「没匹配上」与「真故障」）。
func gitOut(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	// 子进程环境走唯一事实源 procx.ChildEnv，**不继承全量 environ**：本工具在
	// CI 里跑，父进程环境可能带凭据；git 的本地子命令也用不到它们。附带收益是
	// 它默认补 GIT_TERMINAL_PROMPT=0 —— 万一将来有人加了会问凭据的子命令，
	// 也是当场失败而不是把流水线挂死。
	cmd.Env = procx.ChildEnv(nil)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("git %s 失败: %v: %s",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}
