// check.go —— 存储选型判据的编排与**场景侧**判据（issue #424）。
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
// 声明面只能**引用**那些 id：引用表外的 id 即红；矩阵里的能力若没有任何
// 存储认领，也即红（没人认领 = 该能力不被任何场景判定 = 矩阵与选型面之间
// 开了一个洞）。
//
// # 硬边界必须机器判，不能靠碰巧
//
// 「多写者场景不得选单写者档位」这条边界，若只靠 capability 归属（把多进程
// 那几项不声明给单写者档位）撑着，那么**归属一旦被人改错，硬边界就跟着失效**
// ——「归属错了也不红」是最贵的那种形态。
// ⇒ 写者数与写者上限**都是数据**（写在声明面里），门禁代码里不出现任何
// 部署形态词。
//
// # 陈旧声明当天即红
//
// 声明面里没有任何场景引用的 storage ⇒ 红（与仓内「陈旧包根当天即红」同款）。
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/oekit/pkg/errors"
	"gopkg.in/yaml.v3"
)

// check 全部判据的聚合。
//
// 返回 (违反, 说明, 错误)。说明不阻断但必须打出来——派生过程不给人看，
// 人就得自己重推一遍。说明里也带**口径正文**（选型口径必须看得见，
// 否则它就只是一段没人读的散文）。
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

	// 口径侧（I-1）：口径必须看得见 + 必须含关键文本。
	policyProbs, policyNotes := checkPolicy(sel)
	probs = append(probs, policyProbs...)
	notes = append(notes, policyNotes...)

	// 能力侧（判据 3 + 派生标注 + 词表覆盖 + 已知不对称）。
	claimed := map[string]bool{}
	for _, st := range sel.Storages {
		cp, ids := checkStorageCapabilities(root, st, known)
		probs = append(probs, cp...)
		for _, id := range ids {
			claimed[id] = true
		}
		notes = append(notes, fmt.Sprintf("说明 存储 %s（%s）声明 %d 项能力", st.ID, st.Kind, len(st.Capabilities)))
	}
	for _, c := range m.Capabilities {
		if !claimed[c.ID] {
			probs = append(probs, fmt.Sprintf("%s: 能力 %q 没有任何存储认领（该能力不被任何场景判定）",
				selectionPath, c.ID))
		}
	}
	probs = append(probs, checkAsymmetries(root, sel)...)

	// 场景侧（判据 1 + 判据 2 + 硬边界 + 陈旧档位）。
	// 场景说明排在能力说明之后：先看「有哪些档位、各认领几项」，再看
	// 「每个场景选了谁、齐不齐」——读起来是一条从面到点的链。
	scenarioProbs, scenarioNotes := checkScenarios(m, sel, known)
	probs = append(probs, scenarioProbs...)
	notes = append(notes, scenarioNotes...)
	probs = append(probs, checkWriters(m, sel)...)
	probs = append(probs, checkStoragesReferenced(m, sel)...)

	// 已知不对称的摘要：放最后（它是读报告时的补充说明，不是判定结论）。
	for _, a := range sel.Asymmetries {
		notes = append(notes, fmt.Sprintf("已知不对称 %s：%s", a.ID, a.Summary))
	}
	return probs, notes, nil
}

// checkScenarios 逐场景判（判据 1 + 判据 2）。
//
// 返回 (违反, 说明)。**每个场景都产出一条说明**：读门禁输出的人要能逐场景
// 看到「这个场景选了哪个 storage、必需能力齐不齐」——只报违反不给场景全景，
// 输出就少了可追溯性。
//
// 不齐时说明行**点名缺哪几项**：只说「不齐」等于让人自己去重推一遍。
func checkScenarios(m *Matrix, sel *Selection, known map[string]bool) ([]string, []string) {
	var probs []string
	var notes []string
	for _, sc := range m.Scenarios {
		where := fmt.Sprintf("%s: 场景 %s", matrixPath, sc.ID)
		st, ok := findStorage(sel.Storages, sc.Storage)
		if !ok {
			probs = append(probs, fmt.Sprintf("%s: storage %q 不在选型声明面的允许集内", where, sc.Storage))
			notes = append(notes, fmt.Sprintf("说明 场景 %s → storage %s：storage 不在选型声明面的允许集内（必需能力未判）",
				sc.ID, sc.Storage))
			continue
		}
		provided := map[string]bool{}
		for _, c := range st.Capabilities {
			provided[c.ID] = true
		}
		var missing []string
		for _, req := range sc.Required {
			if !known[req] {
				probs = append(probs, fmt.Sprintf("%s: 必需能力 %q 不在部署矩阵的能力表里", where, req))
				continue
			}
			if !provided[req] {
				probs = append(probs, fmt.Sprintf("%s: storage %q 不提供必需能力 %q", where, sc.Storage, req))
				missing = append(missing, req)
			}
		}
		if len(missing) == 0 {
			notes = append(notes, fmt.Sprintf("说明 场景 %s → storage %s：必需能力齐备", sc.ID, sc.Storage))
			continue
		}
		notes = append(notes, fmt.Sprintf("说明 场景 %s → storage %s：必需能力不齐（缺 %s）",
			sc.ID, sc.Storage, strings.Join(missing, "、")))
	}
	return probs, notes
}

// checkWriters 硬边界「多写者场景不得选单写者档位」的数据化判据。
//
// 写者数（场景形态 → 并发写者数）与写者上限（存储 → 上限）**都在声明面里**。
// 门禁代码里不出现任何部署形态词 ⇒ 那些词只能作为**数据**存在。
//
// 为什么必须显式登记写者数：新增场景形态忘了登记 ⇒ 判据无从下手 ⇒ 红
// （不判「没登记」就等于放过那条边界）。
func checkWriters(m *Matrix, sel *Selection) []string {
	var probs []string
	for _, sc := range m.Scenarios {
		if _, ok := sel.ScenarioWriters[sc.Worker]; !ok {
			probs = append(probs, fmt.Sprintf("%s: 场景 %s 的形态 %q 没有登记写者数（写者数是数据，漏登记即判据失效）",
				selectionPath, sc.ID, sc.Worker))
		}
	}
	for _, sc := range m.Scenarios {
		writers, ok := sel.ScenarioWriters[sc.Worker]
		if !ok {
			continue // 已报过
		}
		st, ok := findStorage(sel.Storages, sc.Storage)
		if !ok {
			continue // 判据 1 已报
		}
		limit, unbounded, err := parseMaxWriters(st.MaxWriters)
		if err != nil {
			probs = append(probs, fmt.Sprintf("%s: 存储 %q 的 %v", selectionPath, st.ID, err))
			continue
		}
		if unbounded {
			continue
		}
		if writers > limit {
			probs = append(probs, fmt.Sprintf("%s: 场景 %s 的形态 %q 有 %d 个并发写者，storage %q 最多支持 %d 个",
				selectionPath, sc.ID, sc.Worker, writers, st.ID, limit))
		}
	}
	return probs
}

// checkStoragesReferenced 声明面里没有任何场景引用的 storage ⇒ 红。
//
// 为什么：允许集里的陈旧档位没人拦 ⇒ 它会一直躺在声明面里，直到某天有人
// 以为它还有效。**陈旧声明当天即红**（与仓内「陈旧包根当天即红」同款）。
func checkStoragesReferenced(m *Matrix, sel *Selection) []string {
	var probs []string
	referenced := map[string]bool{}
	for _, sc := range m.Scenarios {
		referenced[sc.Storage] = true
	}
	for _, st := range sel.Storages {
		if !referenced[st.ID] {
			probs = append(probs, fmt.Sprintf("%s: 存储 %q 没有任何场景引用（陈旧声明当天即红）",
				selectionPath, st.ID))
		}
	}
	return probs
}

// parseMaxWriters 解析声明面里的写者上限。
//
// 取值封闭：十进制数字（≥1）或 unlimited（不设上限）。空值是**声明缺失**
// ⇒ 报错（不是「不设上限」）——把「没写」当成「什么都行」正是本门禁要防的
// 那种静默放行。
func parseMaxWriters(v string) (int, bool, error) {
	if v == "" {
		return 0, false, errors.New("max_writers 未声明（必须写数字或 unlimited）")
	}
	if v == writersUnlimited {
		return 0, true, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false, errors.Wrap(err, "max_writers 必须是数字或 unlimited")
	}
	if n < 1 {
		return 0, false, errors.New("max_writers 至少为 1")
	}
	return n, false, nil
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
