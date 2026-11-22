// Package ledgerstatus 从演进台账文档派生「单号 → 状态」。
//
// 生命周期: Experimental（v0.47.0 定级）——internal 内部件，Go 可见性规则已挡住
// 外部引用，无对外兼容承诺；定级理由见 api-lifecycle.yaml，分级规则见
// docs/api-compat.md。
//
// # 为什么需要它
//
// 台账（docs/DISTRIBUTED-EVOLUTION-*.md）此前只在散文里记「这条单清了没」，
// 机器读不出来。代价是台账 §15 形态 (a) 记着的实例：一批单**早已由 8 个 PR
// 合入主干**，而接手的人按「单还挂着 open 标签」判断「还没人做」，
// 整条工作树作废。
//
// 本包是这条派生的**唯一真相**：门禁（cmd/ledgercheck）与部署清单
// （cmd/deploymatrix）都从这里读，谁也不许自己再解析一遍台账——
// 第二份解析必然漂移，而漂移的方向恰好是「门禁判的和台账写的不是一套」。
//
// # 词表与判定面
//
// 台账是 markdown 散文，机器只能靠三处约定：
//
//   - 登记列：表头为 单号 / 单 / 已落单 / 结论单 的列——这一列里的 #N 是
//     **被登记的单号**（台账承认自己在跟这条单）；
//   - 状态列：表头为 状态 的列——这一列给出该行的状态；
//   - 散文标记：正文行里出现「已合入」「仍开着」时，同行的单号继承该状态。
//
// **PR 引用不是单号**：正文里以 PR 形态出现的 #N（落文档 PR #281、
// 合并 PR 分布在 #339 / #340）不进单号视图——它是 PR 号，进视图就等于
// 把两种号混成一种，而两种号撞车时（#331 既是某单的关闭 PR，
// 又是另一处的单号）合并结果就没有意义了。
package ledgerstatus

import (
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/oekit/pkg/errors"
)

// DefaultPath 本仓台账的默认路径（相对仓库根）。
//
// 放在包里而不是各命令各写一份：路径也是会漂的真相，抄两遍就等着它漂。
const DefaultPath = "docs/DISTRIBUTED-EVOLUTION-20261117.md"

// Status 单号在台账里的状态。
type Status string

const (
	// StatusUnknown 派生不出状态：台账既没登记这个单号，也没有任何状态声明。
	//
	// 它**不是**「没有缺口」：调用方必须自己决定派生不出时怎么办——
	// 本仓的纪律是往「仍缺」的方向偏（宁可说还缺，不许默默说齐了）。
	StatusUnknown Status = "unknown"
	// StatusOpen 仍开着：待清偿。
	StatusOpen Status = "open"
	// StatusClosed 已清偿：台账写了「已合入」并给出关闭该单的 PR 号。
	StatusClosed Status = "closed"
)

// OccurrenceKind 出现点的性质。
type OccurrenceKind string

const (
	// OccurrenceRegistry 登记点：登记表里登记该单号的那一行。
	OccurrenceRegistry OccurrenceKind = "registry"
	// OccurrenceReference 引用点：正文里提到该单号（散文，或表格的非登记列）。
	OccurrenceReference OccurrenceKind = "reference"
)

// Occurrence 单号在台账里的一次出现。
type Occurrence struct {
	// Number 单号（不含 #）。
	Number int
	// File 出现所在文件。
	File string
	// Line 出现所在行（1 起）。
	Line int
	// Kind 出现点性质。
	Kind OccurrenceKind
	// Status 该出现点**自带**的状态声明；StatusUnknown 表示此处没有声明
	//（此时不代表该单仍开着，只代表这一处没说话）。
	Status Status
	// ClosingRef 该出现点声明的关闭 PR 号**原文**（形如 "#331"）。
	//
	// 存原文而不是数字是有意的：判据「关闭 PR 号必须形如 #N」要判的就是
	// 这个原文——解析时就把不合法的号丢掉，门禁就永远看不到它。
	ClosingRef string
	// Text 出现点的原始文本（截断）。
	Text string
}

// Entry 一个单号在台账里的合并视图。
type Entry struct {
	// Number 单号（不含 #）。
	Number int
	// Status 合并后的状态（口径见 Ledger.Status）。
	Status Status
	// ClosingPR 合并后的关闭 PR 号（0 = 没有）。
	ClosingPR int
	// Registered 该单号是否出现在登记面（登记表的登记列里）。
	Registered bool
	// Occurrences 全部出现点，按出现顺序。
	Occurrences []Occurrence
}

// ProblemKind 台账结构问题的类别。
type ProblemKind string

const (
	// ProblemTableShape 表格形状不对（列数与表头不齐等）。
	ProblemTableShape ProblemKind = "table_shape"
)

// Problem 台账自身的一处结构问题（指到文件与行）。
type Problem struct {
	// File 问题所在文件。
	File string
	// Line 问题所在行（1 起；0 = 不定位到行）。
	Line int
	// Kind 问题类别。
	Kind ProblemKind
	// Message 人可读说明。
	Message string
}

// Ledger 台账派生出的单号视图。
type Ledger struct {
	file     string
	entries  map[int]*entryData
	problems []Problem
}

// entryData 单号的内部累加器。
type entryData struct {
	occurrences []Occurrence
	registered  bool
}

// Parse 读台账文件并派生单号视图。
//
// 解析是容错的：结构问题收进 Problems() 而不是让整份台账读不出来。
// 只有文件读不到才是错误——那意味着判定面整个失效。
func Parse(path string) (*Ledger, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.Wrap(err, "读台账失败", "path", path)
	}
	return ParseText(string(b), path), nil
}

// ParseText 从台账文本派生单号视图。file 只用于问题的定位与展示。
func ParseText(text, file string) *Ledger {
	l := &Ledger{file: file, entries: map[int]*entryData{}}
	lines := strings.Split(text, "\n")
	blocks := findTableBlocks(lines)
	for _, b := range blocks {
		l.parseTable(lines, b)
	}
	inTable := make(map[int]bool, len(lines))
	for _, b := range blocks {
		for i := b.start; i < b.end; i++ {
			inTable[i] = true
		}
	}
	inCode := fencedCodeLines(lines)
	for i, line := range lines {
		if inTable[i] || inCode[i] {
			continue
		}
		l.parseProse(lines, i, line)
	}
	return l
}

// fencedCodeLines 标出围栏代码块（``` 起止）内的行，供主循环跳过。
//
// **代码块不是台账的叙述**：它是示例与引用——合成数据、被改掉的原文、命令、
// 输出。把它们当声明会凭空造出状态归属：§36 / §37 为了说明「状态标记词的歧义」
// 逐字引用了那句原文，判据当场把 #411 派生成待清偿（而它 2026-11-17 就关了）。
//
// 这个坑连踩三次（§36 正文 / §36 引用原文 / §37 引用原文），每次都是「改措辞
// 绕开」——**靠作者记得改措辞防不住**，故由机制挡：围栏内的行不进单号视图。
//
// 只认**行首**的 ```（允许前导缩进）：正文里夹的行内反引号不是围栏。
// 围栏行本身也跳过（它只是标记，不含声明）。
func fencedCodeLines(lines []string) map[int]bool {
	out := make(map[int]bool, len(lines))
	in := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			in = !in
			out[i] = true
			continue
		}
		if in {
			out[i] = true
		}
	}
	return out
}

// File 返回台账文件路径。
func (l *Ledger) File() string { return l.file }

// Problems 返回解析阶段发现的结构问题。
func (l *Ledger) Problems() []Problem { return l.problems }

// Status 返回单号在台账里的合并状态。
//
// 合并口径：所有出现点里**有状态声明**的那些必须一致；一致则取该状态；
// 不一致、或一处声明都没有 ⇒ StatusUnknown。
//
// 矛盾时不选边：替台账选边就是替它做决定，那正是「派生状态只能有一个真相」
// 要防的事。**发现矛盾是 cmd/ledgercheck 的活**——本包只负责如实呈现事实。
func (l *Ledger) Status(number int) Status {
	e, ok := l.entries[number]
	if !ok {
		return StatusUnknown
	}
	merged := StatusUnknown
	for _, occ := range e.occurrences {
		if occ.Status == StatusUnknown {
			continue
		}
		if merged == StatusUnknown {
			merged = occ.Status
			continue
		}
		if merged != occ.Status {
			return StatusUnknown
		}
	}
	return merged
}

// ClosingPR 返回关闭该单的 PR 号（没有则 ok=false）。
//
// 只认形如 #N 的原文；不合法的原文不进这里（它由门禁当违反报出来）。
func (l *Ledger) ClosingPR(number int) (int, bool) {
	e, ok := l.entries[number]
	if !ok {
		return 0, false
	}
	for _, occ := range e.occurrences {
		if occ.ClosingRef == "" {
			continue
		}
		if n, ok := parseIssueNumber(occ.ClosingRef); ok {
			return n, true
		}
	}
	return 0, false
}

// Registered 该单号是否出现在登记面。
func (l *Ledger) Registered(number int) bool {
	e, ok := l.entries[number]
	return ok && e.registered
}

// Entry 返回单号的合并视图（不存在则 ok=false）。
func (l *Ledger) Entry(number int) (Entry, bool) {
	e, ok := l.entries[number]
	if !ok {
		return Entry{}, false
	}
	out := Entry{
		Number:      number,
		Status:      l.Status(number),
		Registered:  e.registered,
		Occurrences: append([]Occurrence(nil), e.occurrences...),
	}
	if pr, ok := l.ClosingPR(number); ok {
		out.ClosingPR = pr
	}
	return out, true
}

// Entries 返回全部单号的合并视图（按单号升序）。
func (l *Ledger) Entries() []Entry {
	nums := make([]int, 0, len(l.entries))
	for n := range l.entries {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	out := make([]Entry, 0, len(nums))
	for _, n := range nums {
		e, _ := l.Entry(n)
		out = append(out, e)
	}
	return out
}

// add 记一次出现点。
func (l *Ledger) add(occ Occurrence) {
	e, ok := l.entries[occ.Number]
	if !ok {
		e = &entryData{}
		l.entries[occ.Number] = e
	}
	e.occurrences = append(e.occurrences, occ)
	if occ.Kind == OccurrenceRegistry {
		e.registered = true
	}
}

// parseIssueNumber 解析形如 "#331" 的号；不合法的返回 ok=false。
func parseIssueNumber(ref string) (int, bool) {
	if !strings.HasPrefix(ref, "#") {
		return 0, false
	}
	n, err := strconv.Atoi(ref[1:])
	if err != nil {
		return 0, false
	}
	return n, true
}
