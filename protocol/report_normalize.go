package protocol

import (
	"github.com/oekit/pkg/jsonrepair"
)

// 报告域的**枚举位归一**：模型写枚举值时最爱改大小写、加空白，而每个枚举位
// 背后都挂着一个「判错了要付什么代价」的判据。
//
// # 这一组存在的理由：写法差异不该改变语义
//
// 协议里有一组枚举位此前没有归一，后果各不相同，但共同点是**结局由写法决定、
// 不由内容决定**：
//
//   - action（recommendation 内）："None" → 白名单不命中 → **整份报告被拒**，
//     且「有步骤却免审」的矛盾修复不触发；
//   - confidence："High" → **整份报告被拒**；
//   - severity："Critical" → 白名单不命中 → 序降为 0（未评估），聚合时
//     静默不计入，方向恰好是「看起来更轻」；
//   - verdicts[].stance："Adopt" → 条目**被静默丢弃**（连 warn 都没有，
//     事后无法区分「模型没给裁决」与「裁决被大小写吃掉」）；
//   - scope："OUT" → 被翻成 "in"——**语义反转**：模型说「超出范围、要转人工」，
//     解析器记成「在范围内」，而它是路由信号；词表外值（"external" / "转出" /
//     ""）同样曾被兜底成 "in"，现在保持原值交 Validate 裁决；
//   - needs_followup：["True"] → 留下一条内容为 "True" 的**乱码跟进项**
//     （跟进项是要人去执行的东西，混进乱码比少一条更坏）。
//
// 与本文件同目录的 decision / requires_approval 早已各修过一次同类错
// （见 normalizeDecision / normalizeBoolField 的注释）——说明这不是「没想过」，
// 是**逐个字段补**的必然漏网。故本文件把它们收成一处口径：
// **ToLower + TrimSpace**。
//
// # 边界：只收拾写法差异，不改判定
//
// 未知取值**不**被映射成某个已知值（那是判据的事，交给 Validate 的白名单），
// 本文件只保证「同一个词的写法差异」不改变语义。

// normalizeAction action 归一：大小写与空白收敛到白名单形态。
//
// # 旧实现的代价：整份诚实的报告被丢
//
// action 此前是**唯一没有归一**的枚举位，于是模型写 "None" / "NONE" / " none " 时：
//
//   - fixRecommendationContradiction 的 `a == "none"` 精确比较不命中 →
//     「有步骤却免审」的矛盾**没被修**（action 原样留着）；
//   - Validate 的 `actionWhitelist["None"]` 不命中 → critical「action 不在白名单」
//     → **整份报告被拒**（宿主降级去读 markdown 主体）。
//
// 而写成 "none" 一切正常：归一为 "tune" + requires_approval=true，收口通过。
//
// **一个 ASCII 大小写之差决定「收口」还是「整份丢弃」。** 这与同文件的
// normalizeBoolField（"TRUE" / "True" / " true " 炸掉整份解析）是同一类错，
// 那里已经修过一次；action 是同一类错里**漏网的最后一个**。
//
// 归一口径与 decision / scope / confidence 一致：ToLower + TrimSpace。
// 未知取值**不**归一（那是真错，交给 Validate 的白名单判 critical），
// 本函数只收拾「同一个词的写法差异」。
func normalizeAction(m map[string]any, k string, v any) {
	s, ok := v.(string)
	if !ok {
		return
	}
	m[k] = canonicalOrUnset(s, IsActionValid)
}

// normalizeConfidence confidence 归一：大小写与空白收敛到白名单形态。
//
// 同 normalizeAction 的那类错：模型写 "High" / "HIGH" / " high " 时，
// Validate 的 `confidenceAll[...]` 不命中 → critical → **整份报告被拒**。
// 而 "high" 一切正常。confidence 是模型最常写错大小写的枚举位（它只有三个取值，
// 却常被写成 "High"/"HIGH"），而它的判据是 critical——代价是整份丢弃。
func normalizeConfidence(m map[string]any, k string, v any) {
	s, ok := v.(string)
	if !ok {
		return
	}
	m[k] = canonicalOrUnset(s, IsConfidenceValid)
}

// normalizeSeverity severity 归一：大小写与空白收敛到白名单形态。
//
// # 漏网的第六个枚举位（issue #324）
//
// severity 是聚合/看板/分诊的输入，却一直没进本文件的归一清单。模型写
// "Critical" / "HIGH" / "严重" 时白名单不命中 → SeverityRankOf 返回 0
// （未评估）→ MaxSeverity 完全不计入，只在 warn 列表留一条。而同包的
// confidence 对同类写法差异是「归一后通过」，severity 却是「静默降级」——
// 方向恰好是「看起来更轻」（越危险越被算轻）。
//
// 本函数只把**词表内的写法变体**收敛到规范小写；归一后仍词表外的取值
// 保持原值（不臆断成某个档），由 Validate 的 warn 与聚合口径
// （SeverityRankUnknown）负责区分「没填」与「填了但看不懂」。
func normalizeSeverity(m map[string]any, k string, v any) {
	s, ok := v.(string)
	if !ok {
		return
	}
	m[k] = canonicalSeverity(s)
}

// normalizeNeedsFollowupScalar needs_followup 标量形态是布尔（无目标语义）→
// 空数组（jsonrepair ListKeys 通道产出 ["true"]/["false"] 单元素，在此收敛）。
//
// 大小写与空白一并归一：模型写 ["True"] / [" TRUE "] 是同一个意思，
// 不归一的话它会作为一条**内容为 "True" 的跟进项**留在报告里——
// 而跟进项是要人去执行的东西，混进一条乱码比少一条更坏。
func normalizeNeedsFollowupScalar(m map[string]any) {
	if arr, ok := m["needs_followup"].([]any); ok && len(arr) == 1 {
		if s, isStr := arr[0].(string); isStr {
			switch canonicalEnumValue(s) {
			case "true", "false":
				m["needs_followup"] = []any{}
			}
		}
	}
}

// normalizeDecision decision 归一：字符串 → 小写化；白名单（pending_approval/approved/
// rejected）保留；**词表外的取值保持原值**交 Validate 判 critical。对象/数组形态
// （模型自造结构）→ 删除键。
//
// # 为什么不再「一律归空串」（issue #361）
//
// 旧实现（2026-09-25 收敛，自造同义词族打地鼠）把未知值抹成 ""（无决策）。但
// **"不认识" 与 "没声明" 不是同一个零值**：抹成 "" 之后，Validate 的
// 「decision 不在白名单」判据（r.Decision != "" && !IsDecisionValid(...)）就
// **永远够不着它**——同包的一条 fail-closed 红线在解析路径上被 disarm 掉，
// 零告警通过。
//
// 同包的 scope 已在第三轮 #321 改成「保持原值、交 Validate 判 critical」，
// 于是同一包内出现两种相反的口径。现在 decision 与之一致：词表外保持原值，
// 判据面 fail-closed 裁决。
func normalizeDecision(m map[string]any, k string, v any) {
	switch v.(type) {
	case map[string]any, []any:
		delete(m, k)
	default:
		if s, ok := jsonrepair.CoerceString(v); ok {
			c := canonicalEnumValue(s)
			dec := ""
			switch {
			case c == "":
				dec = "" // 未声明：空白等同缺省
			case IsDecisionValid(c):
				dec = c
			case c == "pending": // 常见缩写（实弹：模型输出 "pending"）→ 规范枚举
				dec = DecisionPending
			default:
				// 词表外：**保持原值**，判定交给 Validate（fail-closed，issue #361）。
				// 旧实现在此抹成 ""（无决策）——那把「不认识」与「没声明」混成
				// 同一个零值，Validate 的白名单判据因此永远够不着它。
				dec = s
			}
			m[k] = dec
			// 实弹 2026-09-10（macOS 只读检查）：模型把 pending_approval 当"待处理"用，
			// 而报告无审批语义（requires_approval 缺失）→ 撞 Validate 一致性红线，
			// 整份诚实的检查报告被拦。
			//
			// 旧实现在这里把 decision 抹成 ""（同属「往松处删」）：那条治理红线
			// 因此在解析路径上**永远不可能触发**（decision 能活到 Validate 的前提
			// 就是 requires_approval 为真，那时红线本来就满足）。此处改为**补严**
			// ——与 fixRecommendationContradiction 同向：保住 decision，把审批位补上。
			if canonicalDecision(dec) == DecisionPending && !rawRequiresApproval(m["recommendation"]) {
				ensureRequiresApproval(m)
			}
		}
	}
}

// normalizeScope scope 归一：只把**写法差异**收敛到 in|out 的规范形态；
// 词表外的字符串保持原值，交给 Validate 的白名单判 critical。
//
// # 大小写必须先归一，否则「out」会被翻成「in」
//
// 旧实现只认精确的 "in"/"out"，其余一律落到 "in"。于是模型写 "OUT" / "Out" /
// " out " 时——**它说的是「超出范围、要转人工」，解析器却记成「在范围内」**。
//
// 这不是「宽容解析的副作用」，是**语义反转**：scope 是路由信号（out = 转出/转派），
// 反了之后
//
//   - 报告呈现的口径说「在范围内」，与模型的实际判断相反；
//   - Validate 的「scope=out 时 redirect 必填」不再触发——out 形态缺 redirect
//     会被整份拒（correct），而等价的 "OUT" 形态却畅通无阻（wrong）。
//
// # 词表外不得兜底成 "in"
//
// 旧实现的 default 分支把 "external" / "out_of_scope" / "转出" / "" 全部改写成
// "in"（注释写的是「模型给了检测对象」）——这违反本文件头部的边界：「未知取值
// **不**被映射成某个已知值（那是判据的事，交给 Validate 的白名单）」。解析面
// 把「读不懂」压成「最常见的那一档」，方向恰好是更宽松的那一侧：一份「超出范围、
// 要转人工」的报告被记成「在范围内」。
//
// 故 default 保持原值；Validate 的白名单（scope 不在白名单 → critical）与
// warn 留痕负责裁决。非字符串形态（对象/数组/数字）不是可判定的路由信号：
// 删键（scope 可选），不凭空指定 in/out，也不让严格 unmarshal 因类型不符炸掉
// 整份报告。
//
// 纯空白按**未声明**处理（归一为零值），非空的词表外取值保持原值——判据面用
// TrimSpace 判「是否声明」，两条路径对同一份字节给同一结论。
//
// 归一口径与 decision / action / confidence / stance 一致：ToLower + TrimSpace。
func normalizeScope(m map[string]any, k string, v any) {
	s, ok := v.(string)
	if !ok {
		delete(m, k)
		return
	}
	// 归一形态由**词表**（scopeAll）决定，不再自带第二份 case 清单（issue #382）：
	// 词表新增一个取值而这里漏改 ⇒ 该值在判定面是合法值、在归一面却被判成
	// 词表外 ⇒ 一整个词表内的取值被整份报告 critical 拒掉（更严），且没有任何
	// 判据会红。三处实现（词表 / 归一面 / 判据面）现在同源。
	m[k] = canonicalOrUnset(s, IsScopeValid)
}

// ensureRequiresApproval 把 recommendation.requires_approval 补成 true。
//
// # 为什么是「补严」而不是「删 decision」
//
// 旧实现在 normalizeDecision 里把 pending_approval 抹成 ""（无决策）——那等于
// **消掉**报告自己声明的「待审批」，Validate 的治理红线（decision=pending_approval
// 时 requires_approval 必须为 true）在解析路径上**永远不可能触发**（issue #361）。
//
// 同包的 fixRecommendationContradiction 走的是另一条同向的路：把矛盾**补严**
// （action 规整 + requires_approval=true）。这里对齐它——保住 decision，把审批位
// 补上，判据面照常裁决。
func ensureRequiresApproval(m map[string]any) {
	rec, ok := m["recommendation"].(map[string]any)
	if !ok {
		if _, exists := m["recommendation"]; exists {
			// recommendation 形态不可判（非对象）：交判据面裁决（fail-closed）。
			return
		}
		rec = map[string]any{}
		m["recommendation"] = rec
	}
	rec["requires_approval"] = true
}
