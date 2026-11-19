// check.go —— 存储选型判据（issue #424）。
//
// 判据只有三条，但每一条都必须是**机器判定**的：
//
//  1. 场景声明的 storage 必须在本文件的允许集内；
//  2. 该 storage 必须实际提供场景 required 的全部能力；
//  3. 「storage 提供某能力」的断言必须能在代码里找到落点（path#Symbol 可解析）。
//
// 第 3 条是本门禁与「写份文档让人读」的分界：没有它，「某存储提供租户隔离」
// 就是一句没人验证的话。
//
// # 能力词表不许手抄
//
// 能力 id 的**唯一真相**是部署矩阵的能力表（docs/DEPLOY-MATRIX.yaml）。
// 本文件只能**引用**那些 id：引用表外的 id 即红；矩阵里的能力若没有任何存储
// 认领，也即红（没人认领 = 该能力不被任何场景判定 = 矩阵与选型面之间开了洞）。
//
// # 「存储自证型 / 内核交付型」的标注必须与代码对得上
//
// 一条能力断言的落点要么落在**该存储自己的代码根**下（存储自证型），要么落在
// 内核 / 宿主包里（内核交付型——能力由内核交付，存储只是前提）。标注与代码位置
// 对不上就是声明面在撒谎 ⇒ 即红。空标注（不写 source）也红。
//
// # 「派生不出」必须报红
//
// 能力断言没有落点 ⇒ 判据无从下手 ⇒ 报红，不当「没有手抄」放过。
// 一条都判不了却照常报绿的门禁，是静默空转。
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/oekit/pkg/errors"
	"gopkg.in/yaml.v3"
)

const (
	// matrixPath 部署矩阵（能力词表的唯一真相）。
	matrixPath = "docs/DEPLOY-MATRIX.yaml"
	// selectionPath 选型声明面（本门禁的判定面）。
	selectionPath = "docs/STORAGE-SELECTION.yaml"
)

// 能力来源词表。取值必须与代码落点一致——由判据核对，不靠自觉。
const (
	// sourceStorage 存储自证型：落点在该存储自己的代码根下。
	sourceStorage = "storage"
	// sourceKernel 内核交付型：落点在内核 / 宿主包，存储只是前提。
	sourceKernel = "kernel"
)

// Matrix 部署矩阵（只取本判据要用的字段；其余字段由 deploycheck 管）。
type Matrix struct {
	Version      int          `yaml:"version"`
	Capabilities []Capability `yaml:"capabilities"`
	Scenarios    []Scenario   `yaml:"scenarios"`
}

// Capability 矩阵里的一条能力。
type Capability struct {
	ID    string `yaml:"id"`
	Title string `yaml:"title"`
}

// Scenario 矩阵里的一个部署场景。
type Scenario struct {
	ID       string   `yaml:"id"`
	Title    string   `yaml:"title"`
	Storage  string   `yaml:"storage"`
	Worker   string   `yaml:"worker"`
	Required []string `yaml:"required"`
}

// Selection 选型声明面。
type Selection struct {
	Version     int         `yaml:"version"`
	Policy      Policy      `yaml:"policy"`
	Storages    []Storage   `yaml:"storages"`
	Asymmetries []Asymmetry `yaml:"known_asymmetries"`
}

// Policy 选型口径（散文部分也必须有：判据只管得了对应关系，管不了「为什么」）。
type Policy struct {
	Notes string `yaml:"notes"`
}

// Storage 一条存储及其能力清单。
type Storage struct {
	ID           string              `yaml:"id"`
	Title        string              `yaml:"title"`
	Kind         string              `yaml:"kind"`
	CodeRoot     string              `yaml:"code_root"`
	Notes        string              `yaml:"notes"`
	Capabilities []StorageCapability `yaml:"capabilities"`
}

// StorageCapability 一条「该存储提供该能力」的断言。
type StorageCapability struct {
	ID       string     `yaml:"id"`
	Source   string     `yaml:"source"`
	Evidence []Evidence `yaml:"evidence"`
}

// Evidence 一条可查落点（本门禁只认 contract 形态：path#Symbol）。
type Evidence struct {
	Kind string `yaml:"kind"`
	Ref  string `yaml:"ref"`
}

// Asymmetry 一条**已知不对称**（两侧机制不同但语义同构的那类差异）。
//
// 为什么要显式登记：门禁绿 ≠ 两侧等价。若不写下来，读门禁报告的人会把
// 「绿」读成「没差别」。
type Asymmetry struct {
	ID       string     `yaml:"id"`
	Storages []string   `yaml:"storages"`
	Summary  string     `yaml:"summary"`
	Evidence []Evidence `yaml:"evidence"`
}

// check 三条判据的聚合。
//
// 返回 (违反, 说明, 错误)。说明不阻断但必须打出来——派生过程不给人看，
// 人就得自己重推一遍。
func check(root string) ([]string, []string, error) {
	m, err := loadMatrix(root)
	if err != nil {
		return nil, nil, err
	}
	sel, err := loadSelection(root)
	if err != nil {
		return nil, nil, err
	}

	var probs []string
	var notes []string

	if sel.Version != 1 {
		probs = append(probs, fmt.Sprintf("%s: version 必须为 1，实得 %d", selectionPath, sel.Version))
	}
	if len(m.Capabilities) == 0 {
		probs = append(probs, matrixPath+": 能力表为空（空清单等于没有判据）")
	}
	if len(sel.Storages) == 0 {
		probs = append(probs, selectionPath+": 没有任何存储（空声明面等于没有判据）")
	}
	if strings.TrimSpace(sel.Policy.Notes) == "" {
		probs = append(probs, selectionPath+": policy.notes 为空（选型口径必须写下来：判据只管得了对应关系）")
	}

	known := map[string]bool{}
	for _, c := range m.Capabilities {
		known[c.ID] = true
	}

	storages := map[string]bool{}
	for _, st := range sel.Storages {
		if storages[st.ID] {
			probs = append(probs, fmt.Sprintf("%s: 存储 %q 重复登记", selectionPath, st.ID))
			continue
		}
		storages[st.ID] = true
	}

	// 判据 3 + 派生标注：逐条能力断言核对落点。
	claimed := map[string]bool{}
	for _, st := range sel.Storages {
		cp, ids := checkStorageCapabilities(root, st, known)
		probs = append(probs, cp...)
		for _, id := range ids {
			claimed[id] = true
		}
		notes = append(notes, fmt.Sprintf("存储 %s（%s）声明 %d 项能力", st.ID, st.Kind, len(st.Capabilities)))
	}

	// 能力词表覆盖：矩阵里的每个能力都得有存储认领。
	for _, c := range m.Capabilities {
		if !claimed[c.ID] {
			probs = append(probs, fmt.Sprintf("%s: 能力 %q 没有任何存储认领（该能力不被任何场景判定）",
				selectionPath, c.ID))
		}
	}

	// 判据 1 + 判据 2：逐场景判。
	for _, sc := range m.Scenarios {
		where := fmt.Sprintf("%s: 场景 %s", matrixPath, sc.ID)
		st, ok := findStorage(sel.Storages, sc.Storage)
		if !ok {
			probs = append(probs, fmt.Sprintf("%s: storage %q 不在选型声明面的允许集内", where, sc.Storage))
			continue
		}
		provided := map[string]bool{}
		for _, c := range st.Capabilities {
			provided[c.ID] = true
		}
		missing := 0
		for _, req := range sc.Required {
			if !known[req] {
				probs = append(probs, fmt.Sprintf("%s: 必需能力 %q 不在部署矩阵的能力表里", where, req))
				continue
			}
			if !provided[req] {
				probs = append(probs, fmt.Sprintf("%s: storage %q 不提供必需能力 %q", where, sc.Storage, req))
				missing++
			}
		}
		if missing == 0 {
			notes = append(notes, fmt.Sprintf("场景 %s → storage %s：必需能力齐备", sc.ID, sc.Storage))
		}
	}

	probs = append(probs, checkAsymmetries(root, sel)...)
	if len(sel.Asymmetries) > 0 {
		notes = append(notes, fmt.Sprintf("已登记已知不对称 %d 条", len(sel.Asymmetries)))
	}
	return probs, notes, nil
}

// checkStorageCapabilities 核对一条存储的能力清单（判据 3 + 派生标注）。
//
// 返回 (违反, 该存储认领的能力 id)。
func checkStorageCapabilities(root string, st Storage, known map[string]bool) ([]string, []string) {
	var probs []string
	var ids []string
	for _, c := range st.Capabilities {
		where := fmt.Sprintf("%s: 存储 %s 的能力 %q", selectionPath, st.ID, c.ID)
		ids = append(ids, c.ID)
		if !known[c.ID] {
			probs = append(probs, where+" 不在部署矩阵的能力表里（能力词表不许在本文件里手写）")
		}
		if len(c.Evidence) == 0 {
			probs = append(probs, where+" 没有落点（派生不出：判据无从下手，不当「没有手抄」放过）")
			continue
		}
		switch c.Source {
		case sourceStorage, sourceKernel:
		case "":
			probs = append(probs, where+" 缺 source 标注（存储自证型 / 内核交付型必须显式标注）")
		default:
			probs = append(probs, fmt.Sprintf("%s 的 source 写 %q，只允许 %s / %s",
				where, c.Source, sourceStorage, sourceKernel))
		}
		for _, ev := range c.Evidence {
			ep := where + " 的落点 " + ev.Ref
			if ev.Kind != "contract" {
				probs = append(probs, ep+" 的证据类型只允许 contract")
				continue
			}
			ok, reason := resolveRef(root, ev.Ref)
			if !ok {
				probs = append(probs, ep+" "+reason)
				continue
			}
			path, _, _ := strings.Cut(ev.Ref, "#")
			if c.Source == sourceStorage && !underRoot(st.CodeRoot, path) {
				probs = append(probs, fmt.Sprintf("%s 标注为存储自证型，但落点 %s 不在该存储的代码根 %q 下",
					ep, path, st.CodeRoot))
			}
			if c.Source == sourceKernel && underRoot(st.CodeRoot, path) {
				probs = append(probs, fmt.Sprintf("%s 标注为内核交付型，但落点 %s 就在该存储的代码根 %q 下",
					ep, path, st.CodeRoot))
			}
		}
	}
	return probs, ids
}

// checkAsymmetries 核对已知不对称的登记（落点必须真实存在）。
func checkAsymmetries(root string, sel *Selection) []string {
	var probs []string
	for _, a := range sel.Asymmetries {
		where := fmt.Sprintf("%s: 已知不对称 %q", selectionPath, a.ID)
		if a.Summary == "" {
			probs = append(probs, where+" 缺 summary（不对称必须有一句人能读的话）")
		}
		if len(a.Storages) < 2 {
			probs = append(probs, where+" 必须落在至少两个存储上（不对称是两侧之间的差异）")
		}
		for _, id := range a.Storages {
			if _, ok := findStorage(sel.Storages, id); !ok {
				probs = append(probs, fmt.Sprintf("%s 引用了未登记的存储 %q", where, id))
			}
		}
		if len(a.Evidence) == 0 {
			probs = append(probs, where+" 没有落点（派生不出）")
			continue
		}
		for _, ev := range a.Evidence {
			if ev.Kind != "contract" {
				probs = append(probs, where+" 的证据类型只允许 contract")
				continue
			}
			if ok, reason := resolveRef(root, ev.Ref); !ok {
				probs = append(probs, where+" 的落点 "+ev.Ref+" "+reason)
			}
		}
	}
	return probs
}

// findStorage 按 id 找一条存储。
func findStorage(list []Storage, id string) (Storage, bool) {
	for _, st := range list {
		if st.ID == id {
			return st, true
		}
	}
	return Storage{}, false
}

// underRoot 判 path 是否落在 root 之下（root 为空 ⇒ 谁都不在它下面）。
func underRoot(root, path string) bool {
	if root == "" {
		return false
	}
	return path == root || strings.HasPrefix(path, root+"/")
}

// loadMatrix 读部署矩阵。
//
// 刻意**不**用严格字段：矩阵是另一道门禁（deploycheck）的声明面，它的字段
// 集合由那道门禁管（那边是严格的）⇒ 本门禁再绑一份字段表，只会在别人给它
// 加一个字段时凭空红一场，而那不是「选型错了」。
func loadMatrix(root string) (*Matrix, error) {
	var m Matrix
	if err := decodeYAML(filepath.Join(root, matrixPath), &m, false); err != nil {
		return nil, err
	}
	return &m, nil
}

// loadSelection 读选型声明面。
//
// 这里用**严格字段**：本文件是本门禁独占的声明面，字段名拼错会让整段声明
// 静默失效（读不进去 = 当成没写）⇒ 拼错必须当场红。
func loadSelection(root string) (*Selection, error) {
	var s Selection
	if err := decodeYAML(filepath.Join(root, selectionPath), &s, true); err != nil {
		return nil, err
	}
	return &s, nil
}

// decodeYAML 读一个 YAML 文件并解析（strict 时拒绝未知字段）。
func decodeYAML(path string, out any, strict bool) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return errors.Wrap(err, "读声明面失败", "path", path)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(strict)
	if err := dec.Decode(out); err != nil {
		return errors.Wrap(err, "解析声明面失败", "path", path)
	}
	return nil
}
