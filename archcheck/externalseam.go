// externalseam.go —— 「拿外部量做判定」的函数必须有接缝或显式豁免（issue #482）。
//
// # 这条判据补的是什么缺口
//
// #412 补了 netguard 的另一半（链条面 CheckChainWithResolver），但「判定面与
// 拨号层的接缝清单」从没盘过：哪些函数在问外部世界、其中哪些已有接缝、哪些还
// 没有，**没有一张表** ⇒ 没有一张表就没有「漏了谁」的信号。
//
// # 三类角色，三种处置
//
// 「问外部世界」的函数分三类，处置完全不同：
//
//   - **tool**（执行外部动作本身就是目的）：门禁 CLI 读仓内声明面、跑 git/gh；
//     它们**不该**有接缝——注入了就能绕过真实文件/真实命令（门禁纪律本身要求
//     读真文件、跑真命令）。
//   - **stamp**（把外部量写进记录）：结论不在本层产生（判定在读到该记录的那一层）。
//   - **decide**（拿外部量做判定）：**结论在本层产生** ⇒ 这一类必须有接缝，
//     否则结论随环境漂（机器一忙就假红、磁盘一抖就假绿）。
//
// ⇒ 本判据盯的是**角色判定 + 登记对账**，不是「谁碰了外部 API」。
//
// # 角色为什么必须由人判
//
// 「这个函数拿外部量做没做判定」**推不出来**：同一个形状（`os.Getenv("X") != ""`、
// `time.Now().Add(-ttl)`、`f.mod.Before(cut)`）在不同函数里角色不同；把
// 外部量传进一个会判定的函数（`exceptionExpiry(ex, now)`）更是语法上分不出来。
//
// ⇒ 角色是**人判的**，门禁做的是**对账**：登记的角色/接缝与代码现状必须一致，
// 且每一处外部触达都必须在清单里（漏登记即红、新增触达即红）。
//
// # 判定面
//
//   - **生产代码**（测试夹具不算：它问的是外部世界，但结论由用例自己给）；
//   - 外部依赖类：**clock / env / fs / exec / net**（见 extAPIs）；
//   - 登记面在 docs/EXTERNAL-DEPS.yaml（policy 的 registry 字段指过去）——
//     清单与 policy 分开放：policy 是手写的规则面，清单是**盘点结果**，两者
//     各自有各自的真相。
package archcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ── 外部依赖表 ────────────────────────────────────────────────────────

// extAPI 一条外部依赖 API：import 路径 + API 名 + 依赖类。
type extAPI struct {
	path  string
	name  string
	class string
}

// extAPIs 外部依赖 API 表（按 import 路径认，不按源码文本认 ⇒ 别名导入也判得到）。
//
// **为什么不含 time.After / NewTimer / NewTicker / Sleep**：它们是「等」，不是
// 「读世界」——等多久是**节奏**问题（测试面的墙钟尺子由 #441/#481 那条规则治），
// 读到什么才是「这个函数问到了外部世界」。两者的处置完全不同，混在一起会让
// 清单失真。
var extAPIs = []extAPI{
	// 时钟：读当前时刻 / 读已流逝时长。
	{path: "time", name: "Now", class: "clock"},
	{path: "time", name: "Since", class: "clock"},
	// 环境变量。
	{path: "os", name: "Getenv", class: "env"},
	{path: "os", name: "LookupEnv", class: "env"},
	{path: "os", name: "Environ", class: "env"},
	// 文件系统。
	{path: "os", name: "ReadFile", class: "fs"},
	{path: "os", name: "WriteFile", class: "fs"},
	{path: "os", name: "Stat", class: "fs"},
	{path: "os", name: "Lstat", class: "fs"},
	{path: "os", name: "Open", class: "fs"},
	{path: "os", name: "Create", class: "fs"},
	{path: "os", name: "Mkdir", class: "fs"},
	{path: "os", name: "MkdirAll", class: "fs"},
	{path: "os", name: "Remove", class: "fs"},
	{path: "os", name: "Rename", class: "fs"},
	{path: "os", name: "ReadDir", class: "fs"},
	// 外部程序执行。
	{path: "os/exec", name: "Command", class: "exec"},
	{path: "os/exec", name: "CommandContext", class: "exec"},
	// 出网（本仓当前 0 命中：netguard 已迁 guardx；这一类留给宿主复用）。
	{path: "net", name: "Dial", class: "net"},
	{path: "net", name: "DialTimeout", class: "net"},
	{path: "net", name: "Listen", class: "net"},
	{path: "net", name: "LookupHost", class: "net"},
	{path: "net", name: "LookupIP", class: "net"},
	{path: "net", name: "ParseIP", class: "net"},
	{path: "net/http", name: "Get", class: "net"},
	{path: "net/http", name: "Head", class: "net"},
	{path: "net/http", name: "Post", class: "net"},
	{path: "net/http", name: "NewRequest", class: "net"},
}

// extDepClasses 依赖类的取值域（封闭）。
var extDepClasses = []string{"clock", "env", "fs", "exec", "net"}

// extRoles 角色的取值域（封闭）。
//
//   - tool：执行外部动作本身就是目的 ⇒ 不该有接缝（注入即绕过真实世界）。
//   - stamp：把外部量写进记录 ⇒ 结论不在本层产生。
//   - decide：拿外部量做判定 ⇒ 结论在本层产生 ⇒ 必须有接缝或显式豁免。
var extRoles = []string{"tool", "stamp", "decide"}

// extKnownClass 判一个依赖类是否已知。
func extKnownClass(c string) bool {
	for _, v := range extDepClasses {
		if v == c {
			return true
		}
	}
	return false
}

// extKnownRole 判一个角色是否已知。
func extKnownRole(r string) bool {
	for _, v := range extRoles {
		if v == r {
			return true
		}
	}
	return false
}

// ── 清单（登记面）─────────────────────────────────────────────────────

// ExternalDepRegistry 是外部依赖清单（docs/EXTERNAL-DEPS.yaml）的形状。
//
// 清单是**盘点结果**：每个「问外部世界」的生产函数一行，声明它碰了哪些依赖类、
// 扮演什么角色、有没有接缝。
type ExternalDepRegistry struct {
	Updated string        `yaml:"updated"`
	Deps    []ExternalDep `yaml:"deps"`
}

// ExternalDep 清单里的一行。
type ExternalDep struct {
	Package  string   `yaml:"package"`  // 相对 module 根的目录
	Function string   `yaml:"function"` // 函数名（方法名不带接收者）
	Deps     []string `yaml:"deps"`     // 依赖类（clock/env/fs/exec/net）
	Role     string   `yaml:"role"`     // tool | stamp | decide
	Seam     string   `yaml:"seam"`     // 接缝符号（如 "MemRegistry.now"），可空
	Exempt   string   `yaml:"exempt"`   // 显式豁免理由（role=decide 且无接缝时必填）
	Reason   string   `yaml:"reason"`   // 判定理由（role=decide 时必填）
}

// extKey 清单条目的定位键。
type extKey struct {
	pkg string
	fn  string
}

// extLoadRegistry 读清单文件。
//
// 清单读不到 ⇒ 返回 error（**给不出结论时必须报错**，不许静默当成「没有违反」——
// 那正是本仓最贵的失效形态：门禁变绿）。
func extLoadRegistry(path string) (*ExternalDepRegistry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, fmt.Errorf("外部依赖清单 %s 是空的（空清单等于没有清单，不当作合法输入）", path)
	}
	var reg ExternalDepRegistry
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&reg); err != nil {
		return nil, fmt.Errorf("解析外部依赖清单失败（严格模式：未知键会被 YAML 静默丢弃，"+
			"而一个拼错的键等于一条判不动的登记）: %w", err)
	}
	return &reg, nil
}

// ── 对账 ──────────────────────────────────────────────────────────────

// extViolations 是本规则的执行体入口。
func extViolations(root string, r Rule) ([]Violation, error) {
	if strings.TrimSpace(r.Registry) == "" {
		return nil, fmt.Errorf("规则 %s 声明了 check_kind %s 却没有 registry：清单是它的判定面，"+
			"不声明就一条都判不了（规则在、severity 是 error、报告里照常计入条数）",
			r.ID, CheckKindExternalSeam)
	}
	touches, err := extTouches(root, r)
	if err != nil {
		return nil, err
	}
	reg, err := extLoadRegistry(filepath.Join(root, r.Registry))
	if err != nil {
		return nil, err
	}
	return extReconcile(r, reg, touches, root), nil
}

// extReconcile 清单与代码现状对账。
//
// 三个方向都要判（缺一条，这条判据就是半个）：
//
//  1. **漏登记**：代码里有一处外部触达，清单里没有 ⇒ 红。
//  2. **陈旧/漂移**：清单里的函数没了、依赖变了 ⇒ 红。
//  3. **判定缺失**：role=decide 的条目既没有接缝、也没有显式豁免理由 ⇒ 红；
//     声明的接缝符号在包内不存在 ⇒ 红（接缝声明是空话）。
func extReconcile(r Rule, reg *ExternalDepRegistry, touches []extTouch, root string) []Violation {
	var out []Violation
	add := func(path string, line int, format string, args ...any) {
		out = append(out, Violation{
			Rule: r.ID, Path: path, Line: line,
			Text: fmt.Sprintf(format, args...), Kind: "external_dep_seam",
		})
	}
	// 实测：代码里的 (包, 函数) → 依赖类集合。
	actual := map[extKey]map[string]bool{}
	var actualOrder []extKey
	for _, t := range touches {
		k := extKey{t.pkg, t.fn}
		if _, ok := actual[k]; !ok {
			actual[k] = map[string]bool{}
			actualOrder = append(actualOrder, k)
		}
		actual[k][t.class] = true
	}
	// 清单：(包, 函数) → 条目。
	declared := map[extKey]ExternalDep{}
	for _, d := range reg.Deps {
		declared[extKey{d.Package, d.Function}] = d
	}
	// ① 漏登记。
	for _, k := range actualOrder {
		if _, ok := declared[k]; ok {
			continue
		}
		add(r.Registry, 0,
			"函数 %s 触达了外部依赖（%s）却不在外部依赖清单里："+
				"「哪些函数在问外部世界」没有这张表就没有「漏了谁」的信号——"+
				"请补登记（role/seam/exempt/reason），或跑 make external-deps -update 生成骨架",
			extLabel(k.pkg, k.fn), extClassList(actual[k]))
	}
	// ② 陈旧 / 漂移。
	for _, d := range reg.Deps {
		k := extKey{d.Package, d.Function}
		got, ok := actual[k]
		if !ok {
			add(r.Registry, 0,
				"清单登记的 %s 已不存在（它不再触达任何外部依赖）——"+
					"僵尸登记：留着不删，清单会烂成装饰", extLabel(d.Package, d.Function))
			continue
		}
		want := map[string]bool{}
		for _, c := range d.Deps {
			want[c] = true
		}
		if !extSameSet(want, got) {
			add(r.Registry, 0,
				"清单登记的 %s 的依赖类与代码现状不符：登记 %v，实测 %v——"+
					"登记与事实漂移的方向恰好是「门禁判的和代码跑的不是一套」",
				extLabel(d.Package, d.Function), d.Deps, extClassList(got))
		}
	}
	// ③ 判定缺失（判断方向单独成函数 ⇒ policy 样例能复用同一份判据）。
	out = append(out, extJudgeEntries(r, reg.Deps, root)...)
	sort.Slice(out, func(i, j int) bool { return out[i].Text < out[j].Text })
	return out
}

// extJudgeEntries 判清单的**判断方向**：角色取值域 / decide 角色的接缝或豁免。
//
// 与「覆盖/漂移」那两个方向分开：那两个方向依赖**代码现状**（样例里没有代码），
// 这一层只依赖清单本身 ⇒ policy 样例可以复用它（见 extSampleViolations）。
//
// root 为空时**跳过接缝存在性核验**（样例里的包不存在）⇒ 那一层由 Go 用例钉住。
func extJudgeEntries(r Rule, deps []ExternalDep, root string) []Violation {
	var out []Violation
	add := func(format string, args ...any) {
		out = append(out, Violation{
			Rule: r.ID, Path: r.Registry,
			Text: fmt.Sprintf(format, args...), Kind: "external_dep_seam",
		})
	}
	for _, d := range deps {
		if !extKnownRole(d.Role) {
			add("清单登记的 %s.%s 的 role %q 不在已知取值域 %v 内：取值域必须封闭——"+
				"拼错的角色名会让这条登记静默失效（门禁变绿）",
				d.Package, d.Function, d.Role, extRoles)
			continue
		}
		for _, c := range d.Deps {
			if !extKnownClass(c) {
				add("清单登记的 %s.%s 的依赖类 %q 不在已知取值域 %v 内",
					d.Package, d.Function, c, extDepClasses)
			}
		}
		// **接缝存在性核验对任何声明了接缝的条目都跑**（不限 decide）。
		//
		// 为什么不能只对 decide 跑：本仓登记的接缝大多落在 stamp 角色上
		// （它们是「把外部量写进记录」那一层，顺手把时钟做成可注入），
		// 只对 decide 跑 ⇒ 这段核验在真仓里一次都不执行 ⇒
		// 「门禁绿着却没判」的经典形态（接缝名写错/符号搬走，门禁照绿）。
		if strings.TrimSpace(d.Seam) != "" {
			if root == "" {
				continue
			}
			if err := extSeamExists(root, d.Package, d.Function, d.Seam); err != nil {
				add("清单登记的 %s.%s 声明的接缝 %q 不可用：%v——"+
					"接缝声明必须指向真实存在的符号，否则它是一句空话",
					d.Package, d.Function, d.Seam, err)
			}
		}
		if d.Role != "decide" {
			continue
		}
		switch {
		case strings.TrimSpace(d.Seam) != "":
			// 已在上面核验过接缝存在性。
		case strings.TrimSpace(d.Exempt) == "":
			add("清单登记的 %s.%s 是 decide 角色（拿外部量做判定）却既没有接缝也没有豁免理由——"+
				"「有外部依赖却无接缝」要么补接缝，要么显式登记豁免理由（与 exceptions 同款纪律）",
				d.Package, d.Function)
		case strings.TrimSpace(d.Reason) == "":
			add("清单登记的 %s.%s 的豁免没有写 reason：空理由的豁免等于没判过",
				d.Package, d.Function)
		}
	}
	return out
}

// extSampleViolations 用**同一个判断核**判一条 policy 样例。
//
// 这条规则的样例面是**清单片段**（YAML）而不是源码：它判的是「清单与代码现状
// 的对账」，样例必须能表达「这样一份清单会被判红吗」。
//
// 样例只判**判断方向**：覆盖与漂移那两个方向依赖代码现状，样例里没有代码 ⇒
// 由 Go 用例钉住（见 externalseam_test.go）。
func extSampleViolations(r Rule, text string) ([]Violation, error) {
	var reg ExternalDepRegistry
	dec := yaml.NewDecoder(strings.NewReader(text))
	dec.KnownFields(true)
	if err := dec.Decode(&reg); err != nil {
		return nil, fmt.Errorf("规则 %s 的样例不是可解析的清单片段: %w", r.ID, err)
	}
	return extJudgeEntries(r, reg.Deps, ""), nil
}

// extLabel 渲染一个函数的位置（根目录不显示成 "..Name"）。
func extLabel(pkg, fn string) string {
	if pkg == "" || pkg == "." {
		return fn
	}
	return pkg + "." + fn
}

// extClassList 把依赖类集合渲染成排序后的列表（报告用）。
func extClassList(set map[string]bool) []string {
	var out []string
	for _, c := range extDepClasses {
		if set[c] {
			out = append(out, c)
		}
	}
	return out
}

// extSameSet 判两个集合是否相等。
func extSameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
