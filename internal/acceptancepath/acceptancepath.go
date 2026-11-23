// Package acceptancepath —— 验收路径的「被执行过」证据对账（issue #483）。
//
// 生命周期: Experimental（v0.74.0 定级）——internal 内部件，Go 可见性规则已挡住
// 外部引用，无对外兼容承诺；定级理由见 api-lifecycle.yaml，分级规则见
// docs/api-compat.md。
//
// # 这条判据补的是什么缺口
//
// 真集群类验收很贵（要 docker + kind + 拉镜像），人不会天天跑 ⇒
// 「上次跑是什么时候、哪条路径跑过」没人说得清。而第十八轮台账 §27 实测撞见过
// 「判据代码**从未被执行过**」这一形态（叠加策略那条路径常年 Skipf）——
// 测试在、绿着，但从没跑过。
//
// ⇒ 验收路径要有一张「被执行过」的登记表：每条路径登记**证据**（跑过的时间戳 +
// 结论文件路径），没跑过的路径**显形点名**，而不是靠人记得。
//
// # 五条口径（逐条写在这里）
//
//  1. **从未跑过 = 阻断**（红）：登记了一条没跑过的路径 ⇒ 「跑过一次即算数」的
//     前提不成立 ⇒ 门禁必须逼人真的跑一次。
//  2. **时效只回显，不判红**：跑过一次就算数；过期只回显（与 issue-state 快照
//     同口径——「时间判据会让门禁红在与被判事实无关的原因上」）。
//  3. **入口必须真的存在**：本仓自有的路径，其 Makefile 目标必须存在、且带
//     `# acceptance-path: <id>` 标记 ⇒ 挡住「登记了一条已经不存在的路径」
//     （判据体搬走、入口没跟着搬）。
//  4. **反向对账**：Makefile 里声明过的验收路径必须进登记面 ⇒ 新加的真环境
//     验收入口不会「装不存在」。
//  5. **证据与登记逐字对账**：证据文件里有三行机器可读字段（id / ran_at /
//     conclusion），值与登记不一致即红 ⇒ 挡住「复制别的路径的结论」与
//     「结论改了但登记没改」。
//
// # 判据与数据源分离
//
// 判据是**纯函数**（清单 + 仓内事实 ⇒ 结论）；证据文件的存在与内容由本包自己读
// （那是「仓内事实」，不是外部数据源）。时钟由调用方注入 ⇒ 用例可确定性测。
package acceptancepath

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultStaleAfter 是「回显而不判红」的默认时效。
//
// 取 90 天：验收路径的价值在「它至少被验过一次」，不在「它最近被验过」——
// 真集群验收本来就不可能天天跑（要起集群、装 CNI、拉镜像）。
const DefaultStaleAfter = 90 * 24 * time.Hour

// 证据文件里的机器可读字段行前缀。
//
// 为什么用固定的行前缀而不是「文档里出现过 id 字样」：后者会把「正文里提到
// 这条路径」误判成「这条路径被验过并留了结论」——而这正是本判据要挡的假绿。
// 字段行是**登记面**的唯一形态：结论文件里有一行 `acceptance-path-id: X`
// 才表示「X 被验过，结论见另外两行」。
const (
	// FieldID 证据里的路径 id 行。
	FieldID = "acceptance-path-id:"
	// FieldRanAt 证据里的跑过时间行。
	FieldRanAt = "acceptance-path-ran-at:"
	// FieldConclusion 证据里的结论行。
	FieldConclusion = "acceptance-path-conclusion:"
)

// Registry 是验收路径登记表（docs/acceptance-paths.yaml）的形状。
type Registry struct {
	// Updated 登记面版本戳（YYYY-MM-DD）。
	Updated string `yaml:"updated"`
	// Paths 登记的验收路径。
	Paths []Path `yaml:"paths"`
}

// Path 一条验收路径的登记。
type Path struct {
	// ID 短标识（清单内唯一）。
	ID string `yaml:"id"`
	// Scope 这条路径验的是什么（一句话）。
	Scope string `yaml:"scope"`
	// Command 可复跑入口（如 "make bench"）。
	//
	// 本仓自有的路径必须给 `make <target>` 形态：别的形态入口存在性无从核实，
	// 而「核验跑不起来」按失败即红处理（见 checkEntry）。
	Command string `yaml:"command"`
	// Owner 判据体所在仓（空 = 本仓）。
	//
	// 为什么要写 owner：判据体随边界迁移走时，**入口**必须跟着走；登记里写清
	// owner，读者才知道「这条路径现在归谁跑」，而门禁只对**本仓自有**的路径
	// 核验入口存在性（别仓的入口本仓无从核实 ⇒ 回显不判红）。
	Owner string `yaml:"owner"`
	// Evidence 结论文件路径（相对仓库根）——它同时是「跑过」的证据。
	//
	// 跨仓路径的证据仍放在**本仓**：结论存档随判据体留在内核仓，别仓的仓内
	// 文件本仓无从核实（核验不到 ⇒ 只能靠人记 ⇒ 判据就空了）。
	Evidence string `yaml:"evidence"`
	// RanAt 跑过的时间（YYYY-MM-DD）。
	RanAt string `yaml:"ran_at"`
	// Conclusion 当时的结论（pass / fail）。
	Conclusion string `yaml:"conclusion"`
}

// Options 是判据的输入（时钟与根目录由调用方给 ⇒ 用例可确定性测）。
type Options struct {
	// Root 仓库根（证据文件与 Makefile 都相对它）。
	Root string
	// Now 当前时间（注入 ⇒ 时效判据可确定性测）。
	Now time.Time
	// StaleAfter 超过它就回显；零值取 DefaultStaleAfter。
	StaleAfter time.Duration
	// Makefile 本仓 Makefile 相对路径（核验 command 指向的目标存在）。
	Makefile string
	// ReadFile 读文件接缝（nil = os.ReadFile）。
	//
	// 为什么有接缝：Judge 拿外部量（证据文件 / Makefile）**做判定** ⇒ 按本仓
	// 「判定面接缝」纪律（issue #482），拿外部量判定的函数必须有接缝或显式豁免。
	// 有接缝的另一个好处：用例不必碰磁盘就能钉住判据（不必为「造一份坏登记面」
	// 去写临时文件）。
	ReadFile func(path string) ([]byte, error)
}

// Result 是一次判定的结论。
type Result struct {
	// Problems 阻断级问题（每条都能指到具体路径）。
	Problems []string
	// Fatal 判据**跑不起来**（读不到 Makefile 之类）。
	//
	// 与 Problems 分开：跑不起来不等于「没有违反」——CLI 据此给退出码 2，
	// 不把「查不到」粉饰成「通过」。
	Fatal []string
	// Echoes 只回显不判红（时效过期 / 跨仓入口无从核实）。
	Echoes []string
	// Never 从未跑过的路径 ID（显形点名）。
	Never []string
	// Ran 跑过的路径 ID（按登记顺序）。
	Ran []string
}

// Judge 判一份登记表：哪些路径从未跑过、哪些过期、哪些登记已经与事实脱节。
//
// 判据是**纯函数**（清单 + 仓内事实 ⇒ 结论），不联网、不读外部数据源。
func Judge(reg *Registry, opts Options) Result {
	stale := opts.StaleAfter
	if stale <= 0 {
		stale = DefaultStaleAfter
	}
	makefile := opts.Makefile
	if makefile == "" {
		makefile = "Makefile"
	}
	readFile := opts.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}
	var res Result

	if _, perr := time.Parse("2006-01-02", reg.Updated); perr != nil {
		res.Problems = append(res.Problems, fmt.Sprintf(
			"验收路径登记面的 updated %q 不是 YYYY-MM-DD：登记面自己的版本戳都不可信，"+
				"别人更没法判断它新不新", reg.Updated))
	}

	// 入口索引：Makefile 的目标集合 + 目标 → 它声明的验收路径 id。
	index, merr := readMakeFileIndex(filepath.Join(opts.Root, makefile), readFile)
	if merr != nil {
		res.Fatal = append(res.Fatal, fmt.Sprintf(
			"读不到 %s（%v）：入口存在性这条判据跑不起来——跑不起来的门禁等于没有门禁",
			makefile, merr))
	}

	seen := map[string]bool{}
	for _, p := range reg.Paths {
		id := strings.TrimSpace(p.ID)
		if id == "" {
			res.Problems = append(res.Problems,
				"验收路径登记里有一条没有 id（无法定位，也无法点名）")
			continue
		}
		if seen[id] {
			res.Problems = append(res.Problems, fmt.Sprintf(
				"验收路径 id %s 重复登记：同一个 id 有两条登记 ⇒ 证据与结论对不上时无从定位", id))
			continue
		}
		seen[id] = true

		// ① 从未跑过 = 阻断。
		if strings.TrimSpace(p.Evidence) == "" {
			res.Problems = append(res.Problems, fmt.Sprintf(
				"验收路径 %s 没有登记证据文件：没有证据 ⇒ 它从未被跑过，"+
					"而「跑过一次即算数」的前提不成立", id))
			res.Never = append(res.Never, id)
			continue
		}
		content, err := readFile(filepath.Join(opts.Root, p.Evidence))
		if err != nil {
			res.Problems = append(res.Problems, fmt.Sprintf(
				"验收路径 %s 从未被执行过（证据文件 %s 读不到）："+
					"真集群类验收很贵，人不会天天跑——但「没跑过」必须显形，而不是靠人记得",
				id, p.Evidence))
			res.Never = append(res.Never, id)
			continue
		}
		// ② 证据与登记逐字对账（三行机器可读字段）。
		//
		// **不 continue**：证据对不上**不掩盖**后面的问题（入口在不在、时间戳合不
		// 合法）。一次报全 ⇒ 维护者改一轮就能改完；一条一条冒出来 ⇒ 改 N 轮，
		// 而每一轮都要重新跑一次门禁。
		evidenceOK := true
		if msg, ok := checkEvidenceFields(string(content), p); !ok {
			res.Problems = append(res.Problems, msg)
			evidenceOK = false
		}
		if evidenceOK {
			res.Ran = append(res.Ran, id)
		}

		// ③ 时效只回显，不判红。
		ranAt, perr := time.Parse("2006-01-02", p.RanAt)
		if perr != nil {
			res.Problems = append(res.Problems, fmt.Sprintf(
				"验收路径 %s 的 ran_at %q 不是 YYYY-MM-DD：时间戳是证据的一部分", id, p.RanAt))
		} else if age := opts.Now.Sub(ranAt); age > stale {
			res.Echoes = append(res.Echoes, fmt.Sprintf(
				"验收路径 %s 上次跑于 %s（%d 天前，超过 %d 天的回显阈值）："+
					"时效只回显不判红——跑过一次即算数", id, p.RanAt,
				int(age.Hours()/24), int(stale.Hours()/24)))
		}

		// ④ 入口存在性：只对本仓自有的路径核验。
		if strings.TrimSpace(p.Owner) == "" {
			res.Problems = append(res.Problems, checkEntry(id, p, index)...)
			continue
		}
		res.Echoes = append(res.Echoes, fmt.Sprintf(
			"验收路径 %s 归 %s 跑：入口存在性本仓无从核实（别仓的入口本仓看不到）", id, p.Owner))
	}

	// ⑤ 反向对账：Makefile 里声明过的验收路径必须进登记面。
	for target, id := range index.marks {
		if !seen[id] {
			res.Problems = append(res.Problems, fmt.Sprintf(
				"Makefile 目标 %s 声明的验收路径 %s 没进登记面："+
					"新增真环境验收入口必须登记，否则「跑过没跑过」无从查", target, id))
		}
	}

	sort.Strings(res.Problems)
	sort.Strings(res.Echoes)
	sort.Strings(res.Never)
	return res
}

// checkEvidenceFields 核验证据文件里的三行机器可读字段与登记是否一致。
//
// 返回 (说明, 通过)。三条字段缺一即红——缺字段不是「没有结论」，是「这份证据
// 不是本判据认的证据形态」⇒ 放过去就是让登记面慢慢变成摆设。
func checkEvidenceFields(content string, p Path) (string, bool) {
	fields := parseEvidenceFields(content)
	if v, ok := fields["id"]; !ok {
		return fmt.Sprintf(
			"验收路径 %s 的证据文件 %s 里没有 %s 字段行：正文里提到这条路径 ≠ 它被验过并留了结论",
			p.ID, p.Evidence, FieldID), false
	} else if v != p.ID {
		return fmt.Sprintf(
			"验收路径 %s 的证据文件 %s 里登记的 id 是 %q：证据与登记对不上"+
				"（多半是别的路径的结论被复制过来了）", p.ID, p.Evidence, v), false
	}
	if v, ok := fields["ran-at"]; !ok {
		return fmt.Sprintf(
			"验收路径 %s 的证据文件 %s 里没有 %s 字段行：跑过的时间是证据的一部分",
			p.ID, p.Evidence, FieldRanAt), false
	} else if v != p.RanAt {
		return fmt.Sprintf(
			"验收路径 %s 登记的 ran_at 是 %q，证据文件 %s 里写的是 %q：登记与证据的时间戳对不上",
			p.ID, p.RanAt, p.Evidence, v), false
	}
	if v, ok := fields["conclusion"]; !ok {
		return fmt.Sprintf(
			"验收路径 %s 的证据文件 %s 里没有 %s 字段行：没有结论就不是一次验收",
			p.ID, p.Evidence, FieldConclusion), false
	} else if v != p.Conclusion {
		return fmt.Sprintf(
			"验收路径 %s 登记的结论是 %q，证据文件 %s 里写的是 %q：结论与证据对不上",
			p.ID, p.Conclusion, p.Evidence, v), false
	}
	return "", true
}

// parseEvidenceFields 解析证据文件里的机器可读字段行。
func parseEvidenceFields(content string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		l := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(l, FieldID):
			out["id"] = strings.TrimSpace(strings.TrimPrefix(l, FieldID))
		case strings.HasPrefix(l, FieldRanAt):
			out["ran-at"] = strings.TrimSpace(strings.TrimPrefix(l, FieldRanAt))
		case strings.HasPrefix(l, FieldConclusion):
			out["conclusion"] = strings.TrimSpace(strings.TrimPrefix(l, FieldConclusion))
		}
	}
	return out
}

// checkEntry 核验本仓自有的验收路径：入口目标必须存在、且带 id 标记。
//
// 三种失败形态分开报（合成一条就分不清「登记错了」与「入口没跟上」）：
//
//	目标不存在      ⇒ 判据体搬走了、入口没跟着搬
//	目标存在没标记  ⇒ 双向对账缺一半：登记说它是入口，入口自己没说它是入口
//	标记的 id 不对  ⇒ 入口与登记说的不是同一条路径
func checkEntry(id string, p Path, index makeIndex) []string {
	target, ok := makeTarget(p.Command)
	if !ok {
		return []string{fmt.Sprintf(
			"本仓自有的验收路径 %s 没有给出 make <target> 形态的入口（登记的是 %q）："+
				"入口存在性这条判据跑不起来 ⇒ 跑不起来的门禁等于没有门禁", id, p.Command)}
	}
	if !index.targets[target] {
		return []string{fmt.Sprintf(
			"验收路径 %s 的入口目标 %s 在 Makefile 里不存在：登记了一条不存在的入口"+
				"（判据体搬走了、入口没跟着搬）", id, target)}
	}
	got, marked := index.marks[target]
	if !marked {
		return []string{fmt.Sprintf(
			"验收路径 %s 的入口目标 %s 在 Makefile 里没有 %s 标记："+
				"双向对账缺一半——登记说它是入口，入口自己没说它是入口",
			id, target, MarkPrefix+" "+id)}
	}
	if got != id {
		return []string{fmt.Sprintf(
			"验收路径 %s 的入口目标 %s 上标的验收路径 id 是 %q：入口与登记说的不是同一条路径",
			id, target, got)}
	}
	return nil
}

// makeTarget 从命令里取 Makefile 目标名；不是 make 形态时 ok=false。
//
// **带独立取值的选项**（`-C dir` / `-f file`）必须连取值一起跳过：
// 只跳过 `-` 开头的那个 token，`dir` 会被当成目标名 ⇒ 入口存在性这条判据
// 会去核验一个不叫 dir 的目标 ⇒ 永远红（假红比漏判更吵，但同样是坏判据）。
func makeTarget(command string) (string, bool) {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) < 2 || fields[0] != "make" {
		return "", false
	}
	for i := 1; i < len(fields); i++ {
		f := fields[i]
		if strings.HasPrefix(f, "-") {
			if takesValue(f) {
				i++ // 跳过它的取值
			}
			continue
		}
		return f, true
	}
	return "", false
}

// takesValue 判断一个 make 选项是否带独立取值。
func takesValue(opt string) bool {
	switch opt {
	case "-C", "-f", "-o", "-W", "-I", "-j", "--directory", "--file", "--makefile", "--include-dir":
		return true
	}
	return false
}
