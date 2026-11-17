// report_enum_normalize_test.go —— 「在词表内」的判定必须只有一套口径（issue #371）。
//
// 解析面（report_normalize.go）对枚举值做 ToLower + TrimSpace，谓词面
// （IsStanceValid / IsActionValid / IsDecisionValid / IsScopeValid /
// IsConfidenceValid）此前却是精确查表；同包唯一归一的 IsSeverityValid 是例外。
//
// 后果是**同一份字节，两种裁决**：ParseReport 把 "HIGH" 归一为 high 后通过，
// 而 json.Unmarshal + Validate 或宿主直接调用谓词时判它非法——一个纯大小写
// 差异让整份报告被拒，宿主拿到的错误信息还看不出这只是一次写法变体。
//
// 本文件把「写法差异不该改变语义」钉在**同一条判据**上：
//   - 每个词表的每个合法值，其大小写 / 空白变体都必须过谓词；
//   - 解析路径与直构路径对同一写法必须给同一结论；
//   - 词表外取值仍然拒绝（归一不能变成「什么都收」）。

package protocol

import (
	"strings"
	"testing"
)

// spellingVariants 返回一个取值的写法变体：原值、全大写、首字母大写、
// 前后空白、混合大小写。取值全部是 ASCII，按字节切首字母是安全的。
//
// 变体里只用空格、不用 \t / \n：解析用例把变体直接拼进 JSON 字符串字面量，
// 控制字符会让 payload 本身不合法，测到的就不是归一了。
func spellingVariants(v string) []string {
	if v == "" {
		return []string{""}
	}
	upper := strings.ToUpper(v)
	title := upper[:1] + v[1:]
	return []string{v, upper, title, " " + v + " ", "  " + upper + "  "}
}

// TestPredicatesAcceptSpellingVariants 五个此前不归一的谓词必须与
// IsSeverityValid 同形：先 ToLower + TrimSpace，再查表。
func TestPredicatesAcceptSpellingVariants(t *testing.T) {
	cases := []struct {
		name  string
		all   func() []string
		valid func(string) bool
	}{
		{"stance", AllStances, IsStanceValid},
		{"action", AllActions, IsActionValid},
		{"confidence", AllConfidences, IsConfidenceValid},
		{"decision", AllDecisions, IsDecisionValid},
		{"scope", AllScopes, IsScopeValid},
		{"severity", AllSeverities, IsSeverityValid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			values := c.all()
			if len(values) == 0 {
				t.Fatal("词表为空——枚举出口与判定面脱钩了")
			}
			for _, v := range values {
				for _, variant := range spellingVariants(v) {
					if !c.valid(variant) {
						t.Errorf("合法值 %q 的写法变体 %q 被谓词拒绝（解析面会归一后通过，两面结论相反）",
							v, variant)
					}
				}
			}
			// 归一不是「什么都收」：词表外取值仍然拒绝。
			for _, bad := range []string{"__unknown__", "out_of_scope", "very-high", "REMEDIATE"} {
				if c.valid(bad) {
					t.Errorf("词表外取值 %q 被谓词接受（归一越界）", bad)
				}
			}
		})
	}
}

// TestSeverityEmptyStillValid severity 的空串是「未评估」，合法但不是取值：
// 归一口径不得把它一并拒掉，也不得让它出现在 AllSeverities 里。
func TestSeverityEmptyStillValid(t *testing.T) {
	for _, v := range []string{"", " ", "\t\n"} {
		if !IsSeverityValid(v) {
			t.Errorf("severity 空形态 %q 应合法（未评估）", v)
		}
	}
	if len(AllSeverities()) == 0 {
		t.Fatal("AllSeverities 不得为空")
	}
	for _, v := range AllSeverities() {
		if v == "" {
			t.Fatal("空串不是 severity 取值，不应出现在 AllSeverities 里")
		}
	}
}

// TestPredicateAndParserAgreeOnSpelling 同一份字节在解析路径与直构路径必须
// 得到同一裁决。这是 issue #371 的核心断言：不是「谓词要宽容」，而是
// 「两条路必须是同一条路」。
func TestPredicateAndParserAgreeOnSpelling(t *testing.T) {
	// parse 构造一份带该字段的报告；direct 构造同样内容的直构报告。
	cases := []struct {
		name   string
		values []string
		parse  func(variant string) (*Report, error)
		direct func(variant string) *Report
	}{
		{
			name:   "confidence",
			values: AllConfidences(),
			parse: func(v string) (*Report, error) {
				return ParseReport(`{"agent_name":"X","confidence":"` + v + `","conclusion":"c","summary":"s"}`)
			},
			direct: func(v string) *Report { return &Report{Confidence: v} },
		},
		{
			name:   "scope",
			values: AllScopes(),
			parse: func(v string) (*Report, error) {
				return ParseReport(`{"agent_name":"X","confidence":"high","conclusion":"c","summary":"s",` +
					`"scope":"` + v + `","redirect":"转人工"}`)
			},
			direct: func(v string) *Report {
				return &Report{Confidence: ConfidenceHigh, Scope: v, Redirect: "转人工"}
			},
		},
		{
			name:   "action",
			values: AllActions(),
			parse: func(v string) (*Report, error) {
				return ParseReport(`{"agent_name":"X","confidence":"high","conclusion":"c","summary":"s",` +
					`"recommendation":{"action":"` + v + `","requires_approval":true}}`)
			},
			direct: func(v string) *Report {
				return &Report{Confidence: ConfidenceHigh,
					Recommendation: &Rec{Action: v, RequiresApproval: true}}
			},
		},
		{
			name:   "stance",
			values: AllStances(),
			parse: func(v string) (*Report, error) {
				return ParseReport(`{"agent_name":"X","confidence":"high","conclusion":"c","summary":"s",` +
					`"verdicts":[{"agent":"a1","stance":"` + v + `","reason":"r"}]}`)
			},
			direct: func(v string) *Report {
				return &Report{Confidence: ConfidenceHigh,
					Verdicts: Verdicts{{Agent: "a1", Stance: v}}}
			},
		},
		{
			name:   "severity",
			values: AllSeverities(),
			parse: func(v string) (*Report, error) {
				return ParseReport(`{"agent_name":"X","confidence":"high","conclusion":"c","summary":"s",` +
					`"severity":"` + v + `"}`)
			},
			direct: func(v string) *Report { return &Report{Confidence: ConfidenceHigh, Severity: v} },
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, canonical := range c.values {
				for _, variant := range spellingVariants(canonical) {
					parsed, perr := c.parse(variant)
					if perr != nil {
						t.Fatalf("解析 %q 失败: %v", variant, perr)
					}
					_, parsedErr := parsed.Validate()
					_, directErr := c.direct(variant).Validate()
					if (parsedErr == nil) != (directErr == nil) {
						t.Errorf("写法变体 %q：解析路径 err=%v，直构路径 err=%v（同一份字节两种裁决）",
							variant, parsedErr, directErr)
					}
					if parsedErr != nil || directErr != nil {
						t.Errorf("合法值 %q 的写法变体 %q 被判非法：parse=%v direct=%v",
							canonical, variant, parsedErr, directErr)
					}
				}
			}
			// 词表外取值：解析面保留原值的那几个字段，两条路都必须拒绝。
			//
			// stance 与 decision 不在此列，且各有明文口径：stance 词表外的条目
			// 被宽容解码**丢弃**（条目级容错），decision 词表外被归一为「无决策」
			// （normalizeDecision 的既有语义）——两条都不属于 #371 的写法变体。
			switch c.name {
			case "severity", "stance", "decision":
				return
			}
			parsed, perr := c.parse("__unknown__")
			if perr != nil {
				t.Fatalf("解析词表外取值失败: %v", perr)
			}
			_, parsedErr := parsed.Validate()
			_, directErr := c.direct("__unknown__").Validate()
			if (parsedErr == nil) != (directErr == nil) {
				t.Errorf("词表外取值：解析路径 err=%v，直构路径 err=%v（两面结论相反）",
					parsedErr, directErr)
			}
			if directErr == nil {
				t.Error("词表外取值必须被直构路径拒绝")
			}
		})
	}
}

// TestDirectActionNoneIsNotExecutable 直构报告写 "NONE" 时，除了谓词要认，
// 「是否执行类动作」的语义判定也必须走同一归一：否则 action="NONE"
// 既被白名单接受、又被当成执行类动作要求审批——同一字段两套解读。
func TestDirectActionNoneIsNotExecutable(t *testing.T) {
	for _, action := range []string{ActionNone, "NONE", "None", " none "} {
		r := &Report{Confidence: ConfidenceHigh, Recommendation: &Rec{Action: action}}
		if _, err := r.Validate(); err != nil {
			t.Errorf("action=%q 是「免审」的写法变体，却要求审批: %v", action, err)
		}
	}
}

// TestDirectPendingDecisionSpellingVariants decision 的 pending_approval 变体
// 必须与规范形态同结论：requires_approval=true 时通过，否则同一红线照常触发。
func TestDirectPendingDecisionSpellingVariants(t *testing.T) {
	for _, decision := range []string{DecisionPending, "PENDING_APPROVAL", "Pending_Approval", " pending_approval "} {
		ok := &Report{Confidence: ConfidenceHigh, Decision: decision,
			Recommendation: &Rec{RequiresApproval: true}}
		if _, err := ok.Validate(); err != nil {
			t.Errorf("decision=%q 带审批语义时应通过: %v", decision, err)
		}
		bad := &Report{Confidence: ConfidenceHigh, Decision: decision}
		if _, err := bad.Validate(); err == nil {
			t.Errorf("decision=%q 缺审批语义时必须照常触发红线", decision)
		}
	}
}
