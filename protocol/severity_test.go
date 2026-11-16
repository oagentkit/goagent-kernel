package protocol

import "testing"

// TestSeverityRankOrderIsMonotonic 严重度序必须严格递增，且 critical 是最高。
//
// 这条在导出可变 map 的形态下**测不出来**：宿主改一张表就能重排全序而测试照样绿。
// 收成不可变访问器之后，「序」不再是被外部共享的一份数据，而是本包的承诺，
// 故这里把它钉成用例。
func TestSeverityRankOrderIsMonotonic(t *testing.T) {
	order := []string{SeverityInfo, SeverityLow, SeverityMedium, SeverityHigh, SeverityCritical}
	for i := 1; i < len(order); i++ {
		lo, hi := SeverityRankOf(order[i-1]), SeverityRankOf(order[i])
		if hi <= lo {
			t.Fatalf("%s(%d) 必须重于 %s(%d)", order[i], hi, order[i-1], lo)
		}
	}
}

// TestSeverityRankUnassessedAndUnknown 「未评估」与「读不懂」必须分档。
//
// 空串是未评估（0）；非空但词表外是读不懂（SeverityRankUnknown，高于
// critical）——旧实现把两者都算 0，聚合时读不懂的严重度被静默丢弃，方向
// 恰好是「看起来更轻」。
func TestSeverityRankUnassessedAndUnknown(t *testing.T) {
	if got := SeverityRankOf(""); got != SeverityRankUnassessed {
		t.Fatalf("空串应为未评估 %d，实得 %d", SeverityRankUnassessed, got)
	}
	for _, s := range []string{"nope", "严重", "重大"} {
		if got := SeverityRankOf(s); got != SeverityRankUnknown {
			t.Fatalf("词表外值 %q 应为 %d（读不懂），实得 %d", s, SeverityRankUnknown, got)
		}
	}
	// 大小写/空白变体不是「未知」：归一后命中词表。
	for _, s := range []string{"CRITICAL", "critical "} {
		if got := SeverityRankOf(s); got == SeverityRankUnknown {
			t.Fatalf("%q 是 critical 的写法变体，不得判为未知", s)
		}
	}
}

// TestMaxSeverityUnassessedAndUnknown MaxSeverity 对未评估不敏感，但不得把
// 「读不懂」当成未评估吞掉（保守侧：读不懂按更重处理）。
func TestMaxSeverityUnassessedAndUnknown(t *testing.T) {
	if got := MaxSeverity("", SeverityHigh); got != SeverityHigh {
		t.Fatalf("空 + high 应得 high，实得 %q", got)
	}
	if got := MaxSeverity("nonsense", SeverityHigh); got != "nonsense" {
		t.Fatalf("读不懂 + high 应保留读不懂（不得静默降级），实得 %q", got)
	}
	if got := MaxSeverity("", ""); got != "" {
		t.Fatalf("两个未评估应得空，实得 %q", got)
	}
}

// TestSeverityCaseVariantsRankSame 大小写与空白是写法差异，不得改变严重度序：
// 旧实现让 "Critical" 落进「未评估」（rank 0），聚合时静默丢失最高危。
func TestSeverityCaseVariantsRankSame(t *testing.T) {
	want := SeverityRankOf(SeverityCritical)
	for _, s := range []string{"critical", "Critical", "CRITICAL", " critical ", "CrItIcAl\t"} {
		if got := SeverityRankOf(s); got != want {
			t.Fatalf("SeverityRankOf(%q)=%d，应与 %q 同为 %d", s, got, SeverityCritical, want)
		}
		if !IsSeverityValid(s) {
			t.Fatalf("大小写/空白变体 %q 应判合法（写法差异不改语义）", s)
		}
	}
}

// TestSeverityUnknownDistinctFromUnassessed 「没填」与「填了但看不懂」不得同档：
// 前者是未评估，后者是读不懂；聚合方向不能把读不懂静默算成更轻。
func TestSeverityUnknownDistinctFromUnassessed(t *testing.T) {
	if SeverityRankOf("") != 0 {
		t.Fatalf("空串应为未评估 rank 0，实得 %d", SeverityRankOf(""))
	}
	unknown := SeverityRankOf("严重")
	if unknown == 0 {
		t.Fatal("词表外取值不得与「未评估」同档（rank 0）")
	}
	if unknown <= SeverityRankOf(SeverityCritical) {
		t.Fatalf("词表外取值必须按保守侧聚合（不得低于 critical），实得 rank %d", unknown)
	}
	// 聚合：未评估 + 读不懂 必须保留读不懂，而不是被当成「什么都没填」。
	if got := MaxSeverity("", "严重"); got != "严重" {
		t.Fatalf("MaxSeverity(\"\", \"严重\") 应保留读不懂的取值，实得 %q", got)
	}
}

// TestParseNormalizesSeverity 解析面必须把 severity 的大小写变体收敛到规范
// 形态，且聚合结果不得低于 critical。
func TestParseNormalizesSeverity(t *testing.T) {
	raw := `{"agent_name":"a","domain":"d","conclusion":"c","confidence":"high","severity":"Critical"}`
	r, err := ParseReport(raw)
	if err != nil {
		t.Fatalf("ParseReport: %v", err)
	}
	if r.Severity != SeverityCritical {
		t.Fatalf("severity 应归一为 %q，实得 %q", SeverityCritical, r.Severity)
	}
	warns, verr := r.Validate()
	if verr != nil {
		t.Fatalf("归一后的 severity 不得被拒：%v（warns=%v）", verr, warns)
	}
	if got := MaxSeverity("", r.Severity); SeverityRankOf(got) < SeverityRankOf(SeverityCritical) {
		t.Fatalf("聚合最高严重度不得低于 critical，实得 %q", got)
	}
}
