// types.go —— 声明面的数据结构与词表（issue #424）。
//
// 单独成文件的原因：这套结构是本门禁与**声明面**之间的契约，三条判据
// （能力侧 / 场景侧 / 口径侧）都要读它 ⇒ 放在一个文件里会让每个判据文件
// 都先塞一段类型声明，挤掉判据本身的余量。
package main

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

// writersUnlimited 写者上限词表里的「不设上限」取值。
//
// 为什么用词而不是数字：写者上限是**声明面里的数据**，门禁代码不该出现
// 任何具体数字的魔法含义 ⇒ 用一个封闭的词表达「无上限」，其余取值一律是
// 十进制数字。
const writersUnlimited = "unlimited"

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
	Version int    `yaml:"version"`
	Policy  Policy `yaml:"policy"`
	// ScenarioWriters 场景形态 → 该形态的并发写者数。
	//
	// 为什么写者数是**数据**：硬边界（「多写者场景不得选单写者档位」）必须
	// 机器可判，而门禁代码里不许出现任何部署形态词 ⇒ 形态与写者的对应关系
	// 只能写在声明面里。键 = 部署矩阵里场景的 worker 取值。
	ScenarioWriters map[string]int `yaml:"scenario_writers"`
	Storages        []Storage      `yaml:"storages"`
	Asymmetries     []Asymmetry    `yaml:"known_asymmetries"`
}

// Policy 选型口径（散文部分也必须有：判据只管得了对应关系，管不了「为什么」）。
type Policy struct {
	// Notes 口径正文。门禁会把它**打出来**——口径不让人看得见 = 没写。
	Notes string `yaml:"notes"`
	// RequiredTexts 口径里必须含有的关键文本要点。
	//
	// 为什么逐条列出来而不是只判 Notes 非空：只判非空的话，把口径删到只剩
	// 一句空话也照样绿 ⇒ 派单要的「在输出里让人看得见」那半边就废了。
	// 要点与关键词都是**数据**（门禁代码里不写死任何口径词）。
	RequiredTexts []RequiredText `yaml:"required_texts"`
}

// RequiredText 一条口径要点：口径正文里必须含它的全部关键词。
type RequiredText struct {
	ID string `yaml:"id"`
	// Keywords 必须在 policy.notes 里逐字出现的片段。
	Keywords []string `yaml:"keywords"`
}

// Storage 一条存储及其能力清单。
type Storage struct {
	ID       string `yaml:"id"`
	Title    string `yaml:"title"`
	Kind     string `yaml:"kind"`
	CodeRoot string `yaml:"code_root"`
	// MaxWriters 该存储能同时承受的并发写者数：十进制数字，或 unlimited。
	//
	// 必须**显式**声明：把它当成「没写 = 不限」就是「没写 = 什么都行」，
	// 那正是本门禁要防的那种静默放行。
	MaxWriters   string              `yaml:"max_writers"`
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
