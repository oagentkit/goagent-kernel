package roundguard_test

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ── 终审 I2：矩阵棘轮只挡「改已有小节」，不挡「加新判据」 ──
//
// 为什么要有这道闸（2026-11-21 终审 I2）：上一轮的矩阵棘轮盯的是「spec 的判据
// **锚点**是否都被矩阵钉住」，而锚点是从「编号列表项 / 编号表行」派生的 ⇒
// spec 里**新增一条判据**时，如果它既没有编号、也不落在编号表行里（= 散文判据），
// 或者它是**一整个新小节**，棘轮**一声不响**。实测三条漏网：
//
//	① 判据小节里加一条无编号散文判据 ⇒ 绿；
//	② 加一个新判据小节（§7.3.1）⇒ 绿；
//	③ §10.1 必测判据表新增一行 ⇒ 绿。
//
// 修法：登记面从「编号列表」升级为**结构指纹三件套**——
//
//	(a) spec 全文 `### ` 小节的**集合** = 登记面；
//	(b) 每张「判据」表的**数据行数** = 登记值；
//	(c) 判据小节里的**散文判据**（粗体 + 全角冒号起头的段落）逐条登记，
//	    多一条 / 少一条 / 改一条 ⇒ 红。
//
// 三条都是**纯函数派生 + 登记面比对**：spec 改了 ⇒ 派生值变 ⇒ 与登记面不等 ⇒
// 当天即红。判据写成纯函数 ⇒ 可被「注入 ⇒ 变红」自证。

// specFingerprintSections 登记面 (a)：spec 全文的 `### ` 小节**集合**。
//
// 为什么登记全部小节、而不只登记「矩阵要钉的那 14 个」：小节是判据的**容器**，
// 容器变了（新增 / 改名 / 删除）就是判据面变了 ⇒ 必须显式登记。
var specFingerprintSections = []string{
	"1.1", "1.2", "1.3", "1.4",
	"2.1", "2.2", "2.3",
	"4.1", "4.2",
	"5.1", "5.2", "5.3",
	"6.1", "6.1.1", "6.2", "6.3", "6.4",
	"7.1", "7.2", "7.3", "7.4", "7.5",
	"9.1", "9.2", "9.3", "9.4", "9.5",
	"10.1", "10.2",
}

// specFingerprintTables 登记面 (b)：「判据」表（表头首列 = 判据）的**数据行数**，
// 按所在小节索引。
var specFingerprintTables = map[string]int{
	"10.1": 17,
}

// specFingerprintProse 登记面 (c)：判据小节里的**散文判据**标记串（粗体部分）。
//
// 为什么单列一张表：编号列表项与编号表行已被上一道判定覆盖，散文判据**两种
// 形态都抓不到**——它既没有编号，也没有表结构 ⇒ 它就是上一道判定的漏网面。
var specFingerprintProse = map[string][]string{
	"4.2": {"判定优先级（顺序即语义）"},
	"5.1": {
		"为什么把「强制收尾」「额度用尽」「无进展」也放进结局词表",
		"为什么「没产出」与「报错」分开",
		"零值语义",
	},
	"5.3": {
		"为什么「要工具」排在「给终答」前面",
		"为什么「强制收尾」排在「给终答」前面",
		"为什么「暂停」排在最后",
		"零值形态的处理",
	},
	"6.1": {"`Validate` 与「零值 = 不限」的关系"},
	"6.1.1": {
		"先分清两件事",
		"证据（2026-11-21 逐仓实读）",
		"为什么不能把「软 / 硬」写死成两个字段的语义",
		"缺省 = `PolicyForceFinal`",
		"强制收尾那一步不可否决",
	},
	"6.2": {"回合数 ≠ 模型请求数"},
	"6.3": {"为什么必须调用前检查", "为什么按工具也要有上限"},
	"6.4": {"处置口径决定这条表的适用范围"},
	"7.1": {"归一化由宿主算，内核不猜"},
	"7.3": {
		"「原指纹全等」那一子情形归谁判（重复优先于漂移）",
		"为什么改成「尾部连续成立长度 L」",
		"窗口下限为什么是 2×Repeat+2",
		"定档规则（命中 ≠ 立刻停）",
		"为什么错误螺旋排在重复前面",
		"为什么「空转」排在最后",
	},
	"7.5": {"证据（agentloop 的重复调用处置）", "为什么照抄这个分档"},
}

// proseCriterionRe 散文判据的形态：行首（可带 ⚠️ 标记）粗体 + 全角冒号。
var proseCriterionRe = regexp.MustCompile(`^\s*(?:⚠️\s*)?\*\*([^*]+)\*\*：`)

// specFingerprint spec 的结构指纹（三件套的派生结果）。
type specFingerprint struct {
	sections map[string]bool     // (a) 全部 `### ` 小节
	tables   map[string]int      // (b) 「判据」表：所在小节 → 数据行数
	prose    map[string][]string // (c) 判据小节的散文判据标记串（按出现顺序）
}

// splitTableRow 拆表格行（首尾竖线去掉，逐列 trim）。
func splitTableRow(ln string) []string {
	s := strings.TrimSpace(ln)
	s = strings.TrimPrefix(s, "|")
	s = strings.TrimSuffix(s, "|")
	parts := strings.Split(s, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// isCriterionTableHeader 该行是不是「判据」表的表头（首列 = 判据）。
func isCriterionTableHeader(ln string) bool {
	if !strings.HasPrefix(strings.TrimSpace(ln), "|") {
		return false
	}
	cells := splitTableRow(ln)
	return len(cells) > 0 && cells[0] == "判据"
}

// deriveSpecFingerprint 从 spec 文本派生结构指纹（纯函数）。
func deriveSpecFingerprint(specText string) specFingerprint {
	fp := specFingerprint{
		sections: map[string]bool{},
		tables:   map[string]int{},
		prose:    map[string][]string{},
	}
	lines := strings.Split(specText, "\n")
	inFence := false
	curSec := ""
	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		if strings.HasPrefix(strings.TrimSpace(ln), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if strings.HasPrefix(ln, "### ") {
			curSec = strings.TrimSpace(strings.TrimPrefix(ln, "### "))
			if j := strings.Index(curSec, " "); j > 0 {
				curSec = curSec[:j]
			}
			fp.sections[curSec] = true
			continue
		}
		if strings.HasPrefix(ln, "## ") {
			curSec = ""
			continue
		}
		if isCriterionTableHeader(ln) {
			n := 0
			for j := i + 1; j < len(lines); j++ {
				if !strings.HasPrefix(strings.TrimSpace(lines[j]), "|") {
					break
				}
				n++
			}
			// n 含分隔行 ⇒ 数据行数 = n-1
			if curSec != "" {
				fp.tables[curSec] = n - 1
			}
			continue
		}
		if m := proseCriterionRe.FindStringSubmatch(ln); m != nil {
			if _, ok := fp.prose[curSec]; ok || isCriterionSection(curSec) {
				fp.prose[curSec] = append(fp.prose[curSec], m[1])
			}
		}
	}
	return fp
}

// isCriterionSection 该小节是不是「判据面小节」（矩阵要逐条钉的那批）。
func isCriterionSection(sec string) bool {
	for _, s := range specCriterionSections {
		if s == sec {
			return true
		}
	}
	return false
}

// sameStringSlice 两个字符串切片是否逐字相同（顺序也要求一致）。
func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// violations 返回结构指纹与登记面的差异（空 = 一致）。
func (fp specFingerprint) violations() []string {
	var out []string
	// (a) 小节集合
	registered := map[string]bool{}
	for _, s := range specFingerprintSections {
		registered[s] = true
	}
	for s := range fp.sections {
		if !registered[s] {
			out = append(out, "spec 出现未登记的 `### ` 小节 §"+s+
				"（新增判据小节 ⇒ 登记面必须当天跟上）")
		}
	}
	for s := range registered {
		if !fp.sections[s] {
			out = append(out, "spec 里找不到登记在案的小节 §"+s+"（登记面与 spec 漂了）")
		}
	}
	// (b) 判据表行数
	for sec, n := range fp.tables {
		want, ok := specFingerprintTables[sec]
		if !ok {
			out = append(out, "spec §"+sec+" 出现未登记的「判据」表（登记面与 spec 漂了）")
			continue
		}
		if want != n {
			out = append(out, "spec §"+sec+" 的「判据」表数据行数 = "+strconv.Itoa(n)+
				"，登记值 = "+strconv.Itoa(want))
		}
	}
	for sec := range specFingerprintTables {
		if _, ok := fp.tables[sec]; !ok {
			out = append(out, "spec 里找不到登记在案的「判据」表（§"+sec+"）（登记面与 spec 漂了）")
		}
	}
	// (c) 散文判据
	for sec, marks := range fp.prose {
		want, ok := specFingerprintProse[sec]
		if !ok {
			out = append(out, "spec §"+sec+" 出现未登记的散文判据（散文判据必须逐条登记）")
			continue
		}
		if !sameStringSlice(marks, want) {
			out = append(out, "spec §"+sec+" 的散文判据与登记面不一致（实得 "+
				strconv.Itoa(len(marks))+" 条 / 登记 "+strconv.Itoa(len(want))+" 条）")
		}
	}
	for sec := range specFingerprintProse {
		if _, ok := fp.prose[sec]; !ok {
			out = append(out, "spec §"+sec+" 的散文判据在登记面里找不到（登记面与 spec 漂了）")
		}
	}
	sort.Strings(out)
	return out
}

// injectIntoCriterionSection 在指定 `### ` 小节末尾插入一段文本（返回新文本）。
// 只在测试里用：用于「注入即红」自证，不碰磁盘上的 spec。
func injectIntoCriterionSection(specText, sec, text string) string {
	lines := strings.Split(specText, "\n")
	start, end := -1, len(lines)
	for i, ln := range lines {
		if start < 0 {
			if strings.HasPrefix(ln, "### "+sec+" ") {
				start = i
			}
			continue
		}
		if strings.HasPrefix(lines[i], "### ") || strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	if start < 0 {
		return specText
	}
	out := append([]string{}, lines[:end]...)
	out = append(out, strings.Split(strings.TrimRight(text, "\n"), "\n")...)
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n")
}

// injectCriterionTableRow 在「判据」表的**数据行末尾**插入一行（返回新文本）。
//
// 为什么必须插在表内：表行集合是「表头后连续的竖线行」，插在小节末尾不算数 ⇒
// 插错位置会让用例「注入了却没生效」而假绿。
func injectCriterionTableRow(specText, row string) string {
	lines := strings.Split(specText, "\n")
	for i, ln := range lines {
		if !isCriterionTableHeader(ln) {
			continue
		}
		last := i
		for j := i + 1; j < len(lines); j++ {
			if !strings.HasPrefix(strings.TrimSpace(lines[j]), "|") {
				break
			}
			last = j
		}
		out := append([]string{}, lines[:last+1]...)
		out = append(out, row)
		out = append(out, lines[last+1:]...)
		return strings.Join(out, "\n")
	}
	return specText
}

// TestSpecFingerprintMatchesRegisteredSurface 断言当前 spec 的结构指纹与登记面
// **一致**（正面锚：判据对合规 spec 不得报红）。
func TestSpecFingerprintMatchesRegisteredSurface(t *testing.T) {
	if v := deriveSpecFingerprint(readSpecText(t)).violations(); len(v) > 0 {
		t.Fatalf("spec 结构指纹与登记面不一致：\n  %s", strings.Join(v, "\n  "))
	}
}

// TestSpecFingerprintRejectsInjectedCriterionSection 反向验证 (a)：注入一个**新的
// 判据小节** ⇒ 必须变红（上一轮这道是漏的）。
func TestSpecFingerprintRejectsInjectedCriterionSection(t *testing.T) {
	injected := "### 7.3.1 注入的判据小节\n\n**注入的散文判据**：这是一条新加的判据。\n"
	inj := injectIntoCriterionSection(readSpecText(t), "7.3", injected)
	v := deriveSpecFingerprint(inj).violations()
	if len(v) == 0 {
		t.Fatal("注入新判据小节后判据仍判绿 ⇒ 结构指纹 (a) 是空转的")
	}
	found := false
	for _, s := range v {
		if strings.Contains(s, "未登记的") && strings.Contains(s, "7.3.1") {
			found = true
		}
	}
	if !found {
		t.Errorf("未报出「未登记的小节 §7.3.1」，实际报出：%v", v)
	}
}

// TestSpecFingerprintRejectsInjectedCriterionTableRow 反向验证 (b)：往 §10.1 的
// 必测判据表**加一行** ⇒ 必须变红（上一轮这道是漏的）。
func TestSpecFingerprintRejectsInjectedCriterionTableRow(t *testing.T) {
	row := "| 注入的判据 | 注入的正向用例 | 注入的反向验证 |"
	inj := injectCriterionTableRow(readSpecText(t), row)
	v := deriveSpecFingerprint(inj).violations()
	if len(v) == 0 {
		t.Fatal("往「判据」表加一行后判据仍判绿 ⇒ 结构指纹 (b) 是空转的")
	}
	found := false
	for _, s := range v {
		if strings.Contains(s, "「判据」表数据行数") {
			found = true
		}
	}
	if !found {
		t.Errorf("未报出「判据表数据行数」变化，实际报出：%v", v)
	}
}

// TestSpecFingerprintRejectsInjectedProseCriterion 反向验证 (c)：在判据小节里加一条
// **无编号散文判据** ⇒ 必须变红（上一轮这道是漏的）。
func TestSpecFingerprintRejectsInjectedProseCriterion(t *testing.T) {
	injected := "\n**注入的散文判据**：这是一条没有编号、没有表格结构的散文判据。\n"
	inj := injectIntoCriterionSection(readSpecText(t), "7.3", injected)
	v := deriveSpecFingerprint(inj).violations()
	if len(v) == 0 {
		t.Fatal("注入无编号散文判据后判据仍判绿 ⇒ 结构指纹 (c) 是空转的")
	}
	found := false
	for _, s := range v {
		if strings.Contains(s, "散文判据") {
			found = true
		}
	}
	if !found {
		t.Errorf("未报出「散文判据」变化，实际报出：%v", v)
	}
}
