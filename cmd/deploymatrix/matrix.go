// matrix.go —— 清单装载与判定（issue #277）。
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// matrixPath 清单路径（相对仓库根）。
	matrixPath = "docs/DEPLOY-MATRIX.yaml"
	// usagePath 机器口径块所在文档。
	usagePath = "docs/USAGE.md"

	// 能力状态词表。
	StateAvailable = "available"
	StatePartial   = "partial"
	StateMissing   = "missing"
)

// Matrix 部署配方能力清单（机器可查的唯一声明面）。
type Matrix struct {
	Version      int          `yaml:"version"`
	Capabilities []Capability `yaml:"capabilities"`
	Scenarios    []Scenario   `yaml:"scenarios"`
}

// Capability 一条部署能力及其可查证据。
type Capability struct {
	ID          string     `yaml:"id"`
	Title       string     `yaml:"title"`
	State       string     `yaml:"state"`
	Evidence    []Evidence `yaml:"evidence"`
	DeliveredBy string     `yaml:"delivered_by,omitempty"`
}

// Evidence 一条可查事实。
type Evidence struct {
	Kind string `yaml:"kind"` // contract | gate | issue | doc
	Ref  string `yaml:"ref"`
}

// Scenario 一个目标部署场景。
type Scenario struct {
	ID        string   `yaml:"id"`
	Title     string   `yaml:"title"`
	Storage   string   `yaml:"storage"`
	Worker    string   `yaml:"worker"`
	Required  []string `yaml:"required"`
	Readiness string   `yaml:"readiness"`
	Notes     string   `yaml:"notes,omitempty"`
}

var (
	issueRe    = regexp.MustCompile(`^#\d+$`)
	usageRowRe = regexp.MustCompile("^\\|\\s*`([a-z0-9-]+)`[^|]*\\|\\s*([^|]*?)\\s*\\|\\s*([^|]*?)\\s*\\|[^|]*\\|\\s*([^|]*?)\\s*\\|\\s*$")
)

// loadMatrix 读清单并用严格字段解析（拼错的字段名会让声明面静默失效，
// 必须当场红）。
func loadMatrix(root string) (*Matrix, error) {
	b, err := os.ReadFile(filepath.Join(root, matrixPath))
	if err != nil {
		return nil, fmt.Errorf("读 %s 失败: %w", matrixPath, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var m Matrix
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", matrixPath, err)
	}
	return &m, nil
}

// checkMatrix 全部判据的聚合（只聚合不截断：报错成本在「人要去改」）。
//
// 返回 (违反, 说明, 错误)。说明是「派生不出 / 推导过程里值得说一声」的事，
// 它们**不阻断**判定，但必须打出来——派生状态的理由不给人看，
// 就等于让人自己重推一遍。
func checkMatrix(root string, m *Matrix) ([]string, []string, error) {
	var probs []string
	var notes []string
	if m.Version != 1 {
		probs = append(probs, fmt.Sprintf("%s: version 必须为 1，实得 %d", matrixPath, m.Version))
	}
	issueCorpus, err := loadIssueCorpus(root)
	if err != nil {
		return nil, nil, err
	}
	ledger, err := loadLedger(root)
	if err != nil {
		return nil, nil, err
	}

	caps := map[string]Capability{}
	for i, c := range m.Capabilities {
		where := fmt.Sprintf("%s: capabilities[%d]", matrixPath, i)
		if c.ID == "" {
			probs = append(probs, where+": id 为空")
			continue
		}
		where = fmt.Sprintf("%s: 能力 %s", matrixPath, c.ID)
		if _, dup := caps[c.ID]; dup {
			probs = append(probs, where+": 重复的能力 id")
			continue
		}
		if c.Title == "" {
			probs = append(probs, where+": title 为空（能力必须有一个人可读的名字）")
		}
		evProbs, err := checkEvidence(root, c, issueCorpus)
		if err != nil {
			return nil, nil, err
		}
		probs = append(probs, evProbs...)
		want, stateNotes := computeState(c.Evidence, ledger)
		for _, n := range stateNotes {
			notes = append(notes, fmt.Sprintf("能力 %s 的 %s", c.ID, n))
		}
		if want == "" {
			probs = append(probs, where+": 没有任何 contract/gate/issue 证据（不可查的能力不算能力）")
		} else if c.State != want {
			probs = append(probs, fmt.Sprintf("%s: state 写 %q，按证据应为 %q（状态必须由证据推导，不能手写）",
				where, c.State, want))
		}
		if c.DeliveredBy != "" && !issueCorpus[c.DeliveredBy] {
			probs = append(probs, fmt.Sprintf("%s: delivered_by %q 在 docs/*.md 与 CHANGELOG 中查不到",
				where, c.DeliveredBy))
		}
		caps[c.ID] = c
	}

	seenScenario := map[string]bool{}
	for i, s := range m.Scenarios {
		where := fmt.Sprintf("%s: scenarios[%d]", matrixPath, i)
		if s.ID == "" {
			probs = append(probs, where+": id 为空")
			continue
		}
		where = fmt.Sprintf("%s: 场景 %s", matrixPath, s.ID)
		if seenScenario[s.ID] {
			probs = append(probs, where+": 重复的场景 id")
			continue
		}
		seenScenario[s.ID] = true
		if s.Title == "" || s.Storage == "" || s.Worker == "" {
			probs = append(probs, where+": title/storage/worker 缺一不可")
		}
		if len(s.Required) == 0 {
			probs = append(probs, where+": required 为空（没有能力要求的场景不可判）")
		}
		worst := StateAvailable
		for _, id := range s.Required {
			c, ok := caps[id]
			if !ok {
				probs = append(probs, fmt.Sprintf("%s: required 引用了未登记的能力 %q", where, id))
				continue
			}
			worst = worseState(worst, c.State)
		}
		if s.Readiness != worst {
			probs = append(probs, fmt.Sprintf("%s: readiness 写 %q，按必需能力应为 %q",
				where, s.Readiness, worst))
		}
	}
	if len(m.Scenarios) == 0 {
		probs = append(probs, matrixPath+": 没有任何场景（空清单等于没有判据）")
	}

	usageProbs, err := checkUsageBlock(root, m)
	if err != nil {
		return nil, nil, err
	}
	return append(probs, usageProbs...), notes, nil
}

// checkEvidence 逐条校验证据可解析。
func checkEvidence(root string, c Capability, issueCorpus map[string]bool) ([]string, error) {
	var probs []string
	for i, e := range c.Evidence {
		where := fmt.Sprintf("%s: 能力 %s 的 evidence[%d] (%s %s)", matrixPath, c.ID, i, e.Kind, e.Ref)
		switch e.Kind {
		case "contract":
			if err := checkContractRef(root, e.Ref); err != nil {
				probs = append(probs, where+": "+err.Error())
			}
		case "gate":
			if err := checkGateRef(root, e.Ref); err != nil {
				probs = append(probs, where+": "+err.Error())
			}
		case "issue":
			if !issueRe.MatchString(e.Ref) {
				probs = append(probs, where+": issue 引用必须形如 #123")
			} else if !issueCorpus[e.Ref] {
				probs = append(probs, where+": 单号在 docs/*.md 与 CHANGELOG 中查不到（引用失效）")
			}
		case "doc":
			if _, err := os.Stat(filepath.Join(root, e.Ref)); err != nil {
				probs = append(probs, where+": 文件不存在")
			}
		default:
			probs = append(probs, where+": 未知的证据类型（只允许 contract/gate/issue/doc）")
		}
	}
	return probs, nil
}

// checkContractRef 校验 path#Symbol：文件存在且符号声明在该文件里。
//
// 符号支持顶层类型/函数/变量/常量与「类型.方法」形态；用 go/ast 而不是
// 逐行正则——声明形态有单行/块/别名多种写法。
func checkContractRef(root, ref string) error {
	path, symbol, ok := strings.Cut(ref, "#")
	if !ok || path == "" || symbol == "" {
		return fmt.Errorf("contract 证据必须写成 path#Symbol")
	}
	full := filepath.Join(root, path)
	if _, err := os.Stat(full); err != nil {
		return fmt.Errorf("文件 %s 不存在", path)
	}
	if !strings.HasSuffix(path, ".go") {
		return fmt.Errorf("contract 证据必须指向 .go 文件（%s）", path)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, full, nil, 0)
	if err != nil {
		return fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	declared := declaredSymbols(f)
	if !declared[symbol] {
		return fmt.Errorf("文件 %s 里找不到声明的符号 %s（引用失效）", path, symbol)
	}
	return nil
}

// declaredSymbols 收集一个 Go 文件里声明的一级符号（含「类型.方法」）。
func declaredSymbols(f *ast.File) map[string]bool {
	out := map[string]bool{}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil || len(d.Recv.List) == 0 {
				out[d.Name.Name] = true
				continue
			}
			if recv := receiverName(d.Recv.List[0].Type); recv != "" {
				out[recv+"."+d.Name.Name] = true
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					out[s.Name.Name] = true
				case *ast.ValueSpec:
					for _, n := range s.Names {
						out[n.Name] = true
					}
				}
			}
		}
	}
	return out
}

// receiverName 取接收者类型名（*T / T / pkg.T 都归一到 T）。
func receiverName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.StarExpr:
		return receiverName(e.X)
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	}
	return ""
}

// checkGateRef 校验 Makefile 里存在该目标（门禁/测试目标必须真的可跑）。
func checkGateRef(root, target string) error {
	if target == "" {
		return fmt.Errorf("gate 证据缺目标名")
	}
	b, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		return err
	}
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(target) + `:`)
	if !re.Match(b) {
		return fmt.Errorf("makefile 里没有目标 %q（引用失效）", target)
	}
	return nil
}

// loadIssueCorpus 可查的单号语料：docs/*.md + CHANGELOG.md。
//
// 判据刻意不联网：`issue: #N` 的事实是「这个单号在本仓文档里有落点」，
// 不是「GitHub 上此刻还是 open」——后者是时间点状态，抄进清单就会过期。
func loadIssueCorpus(root string) (map[string]bool, error) {
	files, err := filepath.Glob(filepath.Join(root, "docs", "*.md"))
	if err != nil {
		return nil, err
	}
	files = append(files, filepath.Join(root, "CHANGELOG.md"))
	re := regexp.MustCompile(`#\d+`)
	out := map[string]bool{}
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, m := range re.FindAllString(string(b), -1) {
			out[m] = true
		}
	}
	return out, nil
}

// computeState 由证据形态推导能力状态（禁止手写）。
//
// 口径：**issue 证据只有在对应单号「仍开着」时才算缺口**（台账是单号状态的
// 唯一真相）。已清偿的单不是缺口——把它算成缺口，能力就永远停在 partial，
// 等于对宿主说「你还缺这个」而它其实早有了。
func computeState(evs []Evidence, ledger *ledgerView) (string, []string) {
	strong, gaps := 0, 0
	var notes []string
	for _, e := range evs {
		switch e.Kind {
		case "contract", "gate":
			strong++
		case "issue":
			gap, note := ledger.issueGap(e.Ref)
			if note != "" {
				notes = append(notes, note)
			}
			if gap {
				gaps++
			}
		}
	}
	switch {
	case strong > 0 && gaps == 0:
		return StateAvailable, notes
	case strong > 0:
		return StatePartial, notes
	case gaps > 0:
		return StateMissing, notes
	default:
		return "", notes
	}
}

// worseState 取两个状态里更差的一个（available > partial > missing）。
func worseState(a, b string) string {
	rank := map[string]int{StateAvailable: 0, StatePartial: 1, StateMissing: 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}
