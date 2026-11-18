// policy_wordtable.go —— word_tables 段的声明面（判定词表登记簿）。
//
// # 为什么词表登记是**声明面**而不是内置清单
//
// 词表是**各仓自己的事实**（本仓有本仓的状态词表，宿主有宿主的），
// 而「消费方必须引用而不是手抄」这条纪律是**机制面**。两者分开：
// 换一个仓只需换 policy 一段，执行体一字不改——与 dependency_direction 段同构。
//
// # 为什么 policy 里**不写取值清单**
//
// 取值必须由**代码派生**（表的键 → 常量声明 → 常量取值）。
// policy 里若抄一份取值清单，那份清单自己就是第二份会漂移的真相——
// 而它漂移的方向恰好是「门禁判的和代码跑的不是一套词表」，
// 失败形态是门禁变绿。故 policy 只声明「哪张表、在哪个包、枚举出口叫什么」。
package archcheck

// WordTables 是 word_tables 段的配置。
type WordTables struct {
	// Enabled 段级开关（缺省启用；显式 false 才关）。
	Enabled *bool `yaml:"enabled"`
	// Severity 两条判据的级别（缺省 error）。
	Severity string `yaml:"severity"`
	// Desc 是给人看的那一句。
	Desc string `yaml:"desc"`
	// IgnoreDirs 不参与手抄判定的目录（相对仓库根；含子目录）。
	//
	// 为什么需要它：这条门禁的判定面是「仓里所有包」，而某些目录
	// （参考实现、命令装配层）天然会把词表取值写进数据面而不是判定面。
	// 豁免必须**显式登记**并写明理由——静默跳过等于让该目录重新变成
	// 「没人管的真相」。
	IgnoreDirs []string `yaml:"ignore_dirs"`
	// Tables 登记的词表清单。
	Tables []WordTable `yaml:"tables"`
	// NotRegistered 是**有意不登记**的判定词表候选清单。
	//
	// 为什么要有它：登记面是稀疏的，而稀疏本身没人记——「这张表我们决定
	// 不登记」这句话若只留在某个人的脑子里，下一个人会重新调研一遍，
	// 甚至直接把它登记进去（把几十处假阳引进门禁）。
	// 不登记也要留痕，与 exceptions 段同款纪律。
	NotRegistered []WordTableNotRegistered `yaml:"not_registered"`
}

// WordTableNotRegistered 一张**有意不登记**的判定词表候选。
type WordTableNotRegistered struct {
	// Subject 是这张表的**身份**（哪张表：包 + 表名 + 取值集）。
	Subject string `yaml:"subject"`
	// Reason 是**不登记的理由**（必填）：理由要写「为什么不登记」，
	// 而不是「暂时没排上」——否则它随时会被当成待办而无人复核。
	Reason string `yaml:"reason"`
}

// on 报告词表门禁是否启用（缺省启用）。
func (w WordTables) on() bool { return w.Enabled == nil || *w.Enabled }

// 词表的两类（登记表要能区分它们，否则要么漏判要么噪声）。
const (
	// wordTableKindDecision 判定词表：取值**决定结论**（会话状态、任务状态、
	// 环节类别……）。消费方必须引用主人常量或自有命名，否则红。
	//
	// 为什么默认是它：登记一张表的目的就是「消费方不许各写一份」，
	// 而「不登记就不判」会退化成散文。
	wordTableKindDecision = "decision"
	// wordTableKindInput 输入归一词表：取值是**外部喂进来的词形**
	// （如 protocol 的图表状态词：healthy / down / 正常 / 宕机……）。
	// 消费方自由使用这些字面量——那是输入数据，不是判定依据；
	// 门禁只判两件事：这张表有导出枚举出口，且出口读的是**同一张表**。
	//
	// 为什么要有这一类：把输入词表当判定词表登记会引出成片假阳
	// （实测 protocol 的 43 个图表词形会命中 17 处，大半是 JSON 键、
	// HTTP 响应体这类无关上下文）。而它们同样需要保护——
	// 「这张归一表归 protocol 管、它的出口不许漂」正是要机判的。
	wordTableKindInput = "input"
)

// WordTable 是一张登记的判定词表。
type WordTable struct {
	// Kind 词表类别（缺省 decision）：decision = 取值决定结论；
	// input = 取值只是输入词形（消费方不判，主人与出口受判）。
	Kind string `yaml:"kind"`
	// Pkg 是这张表的**主人包**（相对仓库根的包目录）。
	Pkg string `yaml:"pkg"`
	// Backing 是承载取值的那张表（包级变量名，值是 map 字面量）。
	//
	// 取值 = 这张 map 的**键**：键是常量名，取值在常量声明里，且常量
	// 声明可能是**跨包别名**（statemachine 的 StatusCreated =
	// contract.StatusCreated）——解析要能追过去，否则派生不出取值，
	// 门禁会「一条都判不了却照常报绿」。
	Backing string `yaml:"backing"`
	// Enum 是这张表的**导出枚举出口**（包级函数名，返回切片）。
	//
	// 缺省 Backing 时，取值来源就是它返回的切片字面量。
	// 有 Backing 时，它**必须读同一张表**——读别的 map 就是第二个真相源。
	Enum string `yaml:"enum"`
	// Predicate 是这张表的**判定谓词**（主人包里的包级函数或方法）。
	//
	// 为什么要有这一面：枚举出口读同一张表，只保证「表 ↔ 出口」不漂；
	// 而**判定面**（IsKnownStatus / Valid 这类）是另一个独立读表的地方——
	// 它若另抄一份清单，判定就会与词表分叉，而没有任何判据会红。
	//
	// 声明了就必须守住：谓词不存在、或它不读登记的那张表 ⇒ 当天即红。
	// 没声明 = 这张表没有登记判定面（不判），故宿主要守住自己的判定面
	// 就得在这里写上它——**声明面靠 policy，不靠自觉**。
	//
	// 形态：包级函数写函数名（IsKnownStatus）；方法写类型名.方法名
	// （StepStatus.Valid）。
	Predicate string `yaml:"predicate"`
	// Normalizer 是这张表的**输入归一出口**（仅 kind: input 有意义）：
	// 把任意输入词形归一到档位/规范形态的**导出**函数。
	//
	// 为什么输入归一词表必须有它：没有归一出口，消费方只能**自己**判这个词形
	// ——于是同一个输入词在不同消费方得到不同结论，而这正是本段门禁
	// 要防的漂移（只是发生在输入侧而不是判定侧）。
	//
	// 判据：必须**导出**（宿主要用它；未导出的函数等于没有出口）、
	// 且必须读登记的那张表（归一到哪一档由那张表说了算；另抄一份清单
	// 就是第二个真相源）。
	Normalizer string `yaml:"normalizer"`
	// Family 是这张表的**族前缀**：主人包里名字以它开头的、取值可解析为
	// 非空字符串的常量，都属于这张表。
	//
	// 为什么要有这一面：取值派生只有「表 → 取值」一个方向。主人包**新增**一个
	// 同族常量却忘了加进表 ⇒ 那个新取值的消费侧从此没人管（它不在表里 ⇒
	// 手抄判据判不到它；它不在枚举出口里 ⇒ 宿主看不到它）。
	//
	// 为什么用「前缀」而不是「列出常量名」：列出常量名就是**第二份会漂的真相**
	// ——policy 里抄一份清单，清单自己会漂，而它漂的方向恰好是「门禁判的和
	// 代码跑的不是一套词表」。前缀是**形状**声明，与 backing / enum / predicate 同类。
	Family string `yaml:"family"`
	// FamilyExclude 是族内**有意不进这张表**的一段常量（按前缀豁免）。
	//
	// 为什么要豁免口：同一族里常含**另一张表**的常量（protocol 的 Decision*
	// 同时含 decisionAll 的 3 个与 decisionSources 的 7 个）⇒ 族前缀判不出
	// 「哪些属于哪张表」，必须显式排除，且每个豁免都要写理由。
	//
	// 为什么按前缀而不是按常量名：按名字要把另一张表的常量逐个列出，那正是
	// 上面说的那份会漂的清单；按前缀一段排除即可。
	FamilyExclude []WordTableFamilyExclude `yaml:"family_exclude"`
	// Desc 是给人看的那一句。
	Desc string `yaml:"desc"`
}

// WordTableFamilyExclude 族内一段「有意不进这张表」的常量豁免。
type WordTableFamilyExclude struct {
	// Prefix 是豁免的常量名前缀（写全名则只豁免那一个）。
	Prefix string `yaml:"prefix"`
	// Reason 是豁免理由（**必填**）：没有理由的豁免等于让那族常量重新变成
	// 「没人管的真相」——与 exceptions 段同款纪律。
	Reason string `yaml:"reason"`
}

// wordTableKind 取词表类别（缺省 decision）。
func (t WordTable) wordTableKind() string {
	if t.Kind == "" {
		return wordTableKindDecision
	}
	return t.Kind
}
