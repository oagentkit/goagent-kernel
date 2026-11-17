package protocol

import (
	"strings"

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
// 归一口径与 decision / scope / confidence 一致（normalizeEnum，唯一来源）。
// 未知取值**不**被映射成已知值（那是真错，交给 Validate 的白名单判 critical），
// 本函数只收拾「同一个词的写法差异」。
func normalizeAction(m map[string]any, k string, v any) {
	s, ok := v.(string)
	if !ok {
		return
	}
	m[k] = normalizeEnum(s)
}

// normalizeConfidence confidence 归一：大小写与空白收敛到白名单形态。
//
// 同 normalizeAction 的那类错：模型写 "High" / "HIGH" / " high " 时，
// Validate 的谓词不命中 → critical → **整份报告被拒**。
// 而 "high" 一切正常。confidence 是模型最常写错大小写的枚举位（它只有三个取值，
// 却常被写成 "High"/"HIGH"），而它的判据是 critical——代价是整份丢弃。
func normalizeConfidence(m map[string]any, k string, v any) {
	s, ok := v.(string)
	if !ok {
		return
	}
	m[k] = normalizeEnum(s)
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
	if n := normalizeEnum(s); n != "" && IsSeverityValid(n) {
		m[k] = n
	}
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
			switch strings.ToLower(strings.TrimSpace(s)) {
			case "true", "false":
				m["needs_followup"] = []any{}
			}
		}
	}
}

// normalizeDecision decision 归一：字符串 → 小写化；白名单（pending_approval/approved/
// rejected）保留；**词表外字符串保持原值**（issue #361）。对象/数组形态（模型自造结构）→
// 删除键。
//
// # 为什么词表外不再归空串
//
// 旧实现把「读不懂的 decision」改写成 ""（= 无决策），而 Validate 的护栏是
// `Decision != "" && !IsDecisionValid(...)`——归一化面恰好把未知值变成护栏
// 看不见的零值，两条 fail-closed 红线在解析路径上被 disarm。归一出错的方向
// 必须是「保留原值 + 判据报 critical」，与 scope / action / confidence 同向：
// 信号不能被抹掉，只能被标记。
//
// # pending_approval 的矛盾修复往严处补
//
// decision=pending_approval 而 requires_approval 不为真时，旧实现删掉
// decision（往松处删），Validate 的「pending 必须要求审批」红线成为死条文。
// 现在与 fixRecommendationContradiction 同向补严：有 recommendation 就补
// requires_approval=true；没有 recommendation 时保留 decision，让 Validate
// 报 critical——自相矛盾不得被伪装成「没写决策」。
func normalizeDecision(m map[string]any, k string, v any) {
	switch v.(type) {
	case map[string]any, []any:
		delete(m, k)
	default:
		if s, ok := jsonrepair.CoerceString(v); ok {
			sd := normalizeEnum(s)
			switch {
			case IsDecisionValid(sd):
				m[k] = sd
			case sd == "pending": // 常见缩写（实弹：模型输出 "pending"）→ 规范枚举
				m[k] = DecisionPending
			case sd == "":
				// 空/纯空白 = 未声明（不是「一个读不懂的决策」）。
				m[k] = ""
			default:
				// 词表外：保持原值，交 Validate 判 critical（issue #361）。
				m[k] = s
			}
			// pending_approval 而无 requires_approval 真值：补严而不是删决策
			// （与 fixRecommendationContradiction 同向）。recommendation 缺失/
			// 形态不可修时不臆造结构，保留 decision 由 Validate 报错。
			if m[k] == DecisionPending && !rawRequiresApproval(m["recommendation"]) {
				if rec, ok := m["recommendation"].(map[string]any); ok {
					rec["requires_approval"] = true
				}
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
// 归一口径与 decision / action / confidence / stance 一致：ToLower + TrimSpace。
// 接受集**不另写清单**：直接问 IsScopeValid（词表 = scopeAll = ScopeIn/ScopeOut），
// 词表扩展时归一面自动跟随（issue #382）。
func normalizeScope(m map[string]any, k string, v any) {
	s, ok := v.(string)
	if !ok {
		delete(m, k)
		return
	}
	if st := normalizeEnum(s); IsScopeValid(st) {
		m[k] = st
	} else {
		m[k] = s // 词表外：保持原值，判定交给 Validate
	}
}
