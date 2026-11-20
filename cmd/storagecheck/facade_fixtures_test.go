// facade_fixtures_test.go —— 门面判据的**合成夹具**（与 facade_test.go 的用例分离）。
//
// 为什么单独成文件：facade_test.go 此前把夹具与用例混在一份里，文件顶到 399 行、
// 阈值 400 行 ⇒ 只剩 1 行余量 ⇒ 下一个往该文件加用例的人会被「结构阈值」拦下，
// 而失败形态是「结构债」而不是「用例写错」⇒ 排查方向被带偏。⇒ 夹具整体挪到
// 这里，两个文件各自留出明显余量。
//
// ⚠ 夹具是**合成数据**：两个假门面（pkgalpha / pkgbeta），各暴露同款的面访问器。
// 夹具里的包名与能力名都不指向任何真实领域——判据面是「声明 ↔ 代码 ↔ 证据」的
// 对应关系，那才是本门禁要钉的东西。
//
// ⚠ 挪动**逐字**保留：夹具内容与派生关系（beta 侧仍由 alpha 侧替换包名而来）
// 原样保留 ⇒ 任何断言的语义都不变。
package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// facadeMatrix 合成部署矩阵（两条能力、两个场景）。
const facadeMatrix = `version: 1
capabilities:
  - id: cap-persist
    title: 状态持久化
    requires_multi_writer: false
  - id: cap-shared
    title: 共享写入
    requires_multi_writer: true
scenarios:
  - id: scene-alpha
    title: 甲
    storage: alpha
    worker: single-node
    required: [cap-persist]
  - id: scene-beta
    title: 乙
    storage: beta
    worker: multi-node
    required: [cap-shared]
`

// facadeSelection 合成选型声明面：两个档位各一扇门面，面访问器同款。
//
// alpha 的 Events 面**没有**能力落点 ⇒ 登记在 known_unbacked_accessors；
// beta 的两个面都有落点 ⇒ 不登记。
const facadeSelection = `version: 1
policy:
  notes: |
    合成口径：两侧门面同款。
    审计路径必须走独立句柄。
  required_texts:
    - id: demo-boundary
      keywords: ["独立句柄"]
scenario_writers:
  single-node: 1
  multi-node: 2
storages:
  - id: alpha
    title: 合成档位甲
    kind: memory
    code_root: pkgalpha
    max_writers: 1
    facade:
      package: pkgalpha
      type: Store
      accessors: [Board, Events]
      other_methods: [Apply]
    capabilities:
      - id: cap-persist
        source: storage
        evidence:
          - kind: contract
            ref: pkgalpha/store.go#Store.Board
  - id: beta
    title: 合成档位乙
    kind: postgresql
    code_root: pkgbeta
    max_writers: unlimited
    facade:
      package: pkgbeta
      type: Store
      accessors: [Board, Events]
      other_methods: [Apply]
    capabilities:
      - id: cap-persist
        source: storage
        evidence:
          - kind: contract
            ref: pkgbeta/store.go#Store.Board
      - id: cap-shared
        source: storage
        evidence:
          - kind: contract
            ref: pkgbeta/store.go#Store.Events
known_unbacked_accessors:
  - storage: alpha
    accessor: Events
    reason: 合成夹具：能力表里没有以事件面为落点的能力。
`

// facadeAlphaSrc / facadeBetaSrc 合成代码：门面类型 + 两个面访问器 + 一个其它方法。
const facadeAlphaSrc = `package pkgalpha

// Store 合成门面类型。
type Store struct{}

// Board 合成面访问器。
func (s *Store) Board() {}

// Events 合成面访问器。
func (s *Store) Events() {}

// Apply 合成其它导出方法。
func (s *Store) Apply() error { return nil }
`

// facadeBetaSrc 合成代码：与 alpha 侧**同款**（只换包名 ⇒ 两侧形状可比）。
var facadeBetaSrc = strings.ReplaceAll(facadeAlphaSrc, "pkgalpha", "pkgbeta")

// facadeRoot 造一个带两扇合成门面的仓根。
func facadeRoot(t *testing.T, selection string) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docs", "DEPLOY-MATRIX.yaml"), facadeMatrix)
	mustWrite(t, filepath.Join(root, "docs", "STORAGE-SELECTION.yaml"), selection)
	mustWrite(t, filepath.Join(root, "pkgalpha", "store.go"), facadeAlphaSrc)
	mustWrite(t, filepath.Join(root, "pkgbeta", "store.go"), facadeBetaSrc)
	return root
}

// facadeOnly 只跑门面侧判据（隔离其它判据的噪声）。
func facadeOnly(t *testing.T, root string) []string {
	t.Helper()
	sel, err := loadSelection(root)
	if err != nil {
		t.Fatalf("读声明面失败: %v", err)
	}
	probs, _ := checkFacades(root, sel)
	return probs
}

// facadeUnbackedBlock 合成登记面（alpha 的 Events 面无能力落点）。
const facadeUnbackedBlock = `known_unbacked_accessors:
  - storage: alpha
    accessor: Events
    reason: 合成夹具：能力表里没有以事件面为落点的能力。
`

// facadeAlphaFacadeBlock 合成门面声明段（alpha 的门面）。
const facadeAlphaFacadeBlock = `    facade:
      package: pkgalpha
      type: Store
      accessors: [Board, Events]
      other_methods: [Apply]
`

// facadeSharedStorage 合成「共用同一扇门」的另一个存储（清单故意不同）。
const facadeSharedStorage = `  - id: beta-shared
    title: 合成档位乙的部署形态
    kind: shared-authority
    code_root: pkgbeta
    max_writers: unlimited
    facade:
      package: pkgbeta
      type: Store
      accessors: [Board]
      other_methods: [Apply]
    capabilities:
      - id: cap-shared
        source: storage
        evidence:
          - kind: contract
            ref: pkgbeta/store.go#Store.Events
`

// facadeAlphaShapeSrc 合成代码：门面类型**声明那一行**由用例替换（三种形态）——三种形态都让
// 「派生集不完整」：① struct 字面量**含嵌入字段** ⇒ 其方法被提升；② **类型别名**（type Store =
// storeImpl）/ ③ **定义类型**（type Store storeImpl）⇒ 解析不出 struct 字段 ⇒ 嵌入字段看不见
// ⇒ 外部包真能调 s.ExtraPromoted() ⇒ 旧实现（只认 struct 字面量）门禁全绿。
const facadeAlphaShapeSrc = `package pkgalpha

// Extra 合成嵌入类型（它的导出方法会被提升到门面类型上）。
type Extra struct{}

// ExtraPromoted 提升方法（外部包可经门面类型调到它）。
func (e Extra) ExtraPromoted() {}

// storeImpl 合成实现体（含嵌入字段 ⇒ 提升方法 ⇒ 派生集不完整）。
type storeImpl struct{ Extra }

// Store 合成门面类型（形态由用例替换：含嵌入字段 / 类型别名 / 定义类型）。
type Store struct{ Extra }

// Board 合成面访问器。
func (s *Store) Board() {}

// Events 合成面访问器。
func (s *Store) Events() {}

// Apply 合成其它导出方法。
func (s *Store) Apply() error { return nil }
`

// facadeSingleMatrix 合成部署矩阵：一条能力、一个场景（**单扇门面的最小形态**）。
//
// ⚠ 写者数两张表必须自洽（W1/W2）：一条 requires_multi_writer: false 的能力 +
// 一个 worker: single-node 的场景 + scenario_writers: {single-node: 1} +
// 存储 max_writers: 1 ⇒ 两侧都判「单写者」⇒ 不触发任何写者数判据。
const facadeSingleMatrix = `version: 1
capabilities:
  - id: cap-persist
    title: 状态持久化
    requires_multi_writer: false
scenarios:
  - id: scene-single
    title: 单节点
    storage: alpha
    worker: single-node
    required: [cap-persist]
`

// facadeSingleSelection 合成选型声明面：**恰好一扇门面**。
//
// ⚠ 刻意只声明一扇门：F3 的判定面是「**两侧**门面的面访问器集合相等」⇒ 只剩
// 一扇门时两侧比对退化成**自我比较（差集恒空 ⇒ 空真）**⇒ 这一侧门面根本不被判。
// ⇒ 单扇门是**合法形态**（门禁不该硬编码「必须两扇门」）⇒ 声明面本身必须绿。
//
// F4 的两条腿都要用到：Board 靠**能力证据**覆盖，Events 靠**登记**覆盖
// ⇒ 两个面访问器都不缺落点 ⇒ 门面覆盖面不红。
const facadeSingleSelection = `version: 1
policy:
  notes: |
    合成口径：只有一扇门面时 F3 没有判定面。
    审计路径必须走独立句柄。
  required_texts:
    - id: demo-boundary
      keywords: ["独立句柄"]
scenario_writers:
  single-node: 1
storages:
  - id: alpha
    title: 合成档位甲
    kind: memory
    code_root: pkgalpha
    max_writers: 1
    facade:
      package: pkgalpha
      type: Store
      accessors: [Board, Events]
      other_methods: [Apply]
    capabilities:
      - id: cap-persist
        source: storage
        evidence:
          - kind: contract
            ref: pkgalpha/store.go#Store.Board
known_unbacked_accessors:
  - storage: alpha
    accessor: Events
    reason: 合成夹具：能力表里没有以事件面为落点的能力。
`

// facadeSingleRoot 造一个**恰好一扇门面**的仓根（不落 pkgbeta ⇒ 门面数 = 1）。
func facadeSingleRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docs", "DEPLOY-MATRIX.yaml"), facadeSingleMatrix)
	mustWrite(t, filepath.Join(root, "docs", "STORAGE-SELECTION.yaml"), facadeSingleSelection)
	mustWrite(t, filepath.Join(root, "pkgalpha", "store.go"), facadeAlphaSrc)
	return root
}
