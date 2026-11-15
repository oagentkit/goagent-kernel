package delta

// 增量节流器单测（真流式观测面）：窗口合并 / seq 单调 / 权威吸收 / 调用轮冲刷 /
// 总量帽 / 收口冲刷全残尾。now 注入免真实等待。

import (
	"testing"
	"time"
)

func TestDeltaPacerWindowMerge(t *testing.T) {
	p := New()
	t0 := time.Now()
	p.now = func() time.Time { return t0 }

	// 窗口内小分片：不发布
	for i := 0; i < 3; i++ {
		if _, ok := p.Offer("reasoning", 1, "思"); ok {
			t.Fatal("窗口内不应发布")
		}
	}
	// 超 200 字符阈值：立即发布（seq 自 1）
	big := ""
	for i := 0; i < 25; i++ {
		big += "八十字符八十字符"
	}
	d, ok := p.Offer("reasoning", 1, big)
	if !ok || d.Seq != 1 {
		t.Fatalf("阈值触发应发布 seq=1: %+v ok=%v", d, ok)
	}
	// 时间推进 400ms：小分片也发布
	t0 = t0.Add(400 * time.Millisecond)
	d, ok = p.Offer("reasoning", 1, "尾")
	if !ok || d.Seq != 2 || d.Text != "尾" {
		t.Fatalf("窗口到期应发布 seq=2: %+v ok=%v", d, ok)
	}
}

func TestDeltaPacerAbsorbAndFlushOnToolCall(t *testing.T) {
	p := New()
	t0 := time.Now()
	p.now = func() time.Time { return t0 }

	if _, ok := p.Offer("text", 1, "前导正文"); ok {
		t.Fatal("窗口内不应发布")
	}
	// 权威快照到达：缓冲吸收，后续迟到分片丢弃
	p.Absorb("text", 1)
	if d, ok := p.Flush("text", 1); ok {
		t.Fatalf("吸收后不应再发布: %+v", d)
	}
	if _, ok := p.Offer("text", 1, "迟到"); ok {
		t.Fatal("吸收后的迟到分片应丢弃")
	}

	// 调用轮正文（无权威快照）：tool_call 收口前冲刷残尾
	if _, ok := p.Offer("text", 2, "调用轮前导"); ok {
		t.Fatal("窗口内不应发布")
	}
	d, ok := p.Flush("text", 2)
	if !ok || d.Seq != 1 || d.Text != "调用轮前导" {
		t.Fatalf("调用轮残尾冲刷应发布: %+v ok=%v", d, ok)
	}
}

func TestDeltaPacerFlushAllAndCap(t *testing.T) {
	p := New()
	t0 := time.Now()
	p.now = func() time.Time { return t0 }

	// 多轮多类残尾：flushAll 按 (轮升序, reasoning→text) 冲刷
	_, _ = p.Offer("text", 1, "残1")
	_, _ = p.Offer("reasoning", 2, "残2")
	_, _ = p.Offer("text", 2, "残3")
	got := p.FlushAll()
	if len(got) != 3 {
		t.Fatalf("应冲刷 3 残尾: %+v", got)
	}
	want := []struct {
		kind string
		it   int
		text string
	}{{"text", 1, "残1"}, {"reasoning", 2, "残2"}, {"text", 2, "残3"}}
	for i, w := range want {
		if got[i].Kind != w.kind || got[i].Iteration != w.it || got[i].Text != w.text {
			t.Fatalf("flushAll[%d] = %+v，期望 %v", i, got[i], w)
		}
	}

	// 总量帽：64KB 后静默停发
	p2 := New()
	p2.now = func() time.Time { return t0 }
	chunk := make([]byte, 4096)
	for i := range chunk {
		chunk[i] = 'a'
	}
	published := 0
	for i := 0; i < 100; i++ {
		// 每片 4KB、强制过窗（时间推进）
		t0 = t0.Add(400 * time.Millisecond)
		if _, ok := p2.Offer("reasoning", 1, string(chunk)); ok {
			published += 4096
		}
	}
	if published > CapBytes+4096 {
		t.Fatalf("应被总量帽拦住: published=%d cap=%d", published, CapBytes)
	}
}

// TestDeltaPacerFlushAllZeroBasedIteration 0 基 iteration 的残尾不被丢
// （issue #153）：此前 FlushAll 从 it=1 起扫、max 初值 0——仅 0 轮时循环体
// 一次不进，0 轮残尾静默不发布；多轮时 0 轮被跳过。iteration 的下界契约
// 未钉（约定从 1 起），换 0 基宿主即静默丢流式尾巴。
func TestDeltaPacerFlushAllZeroBasedIteration(t *testing.T) {
	t0 := time.Now()

	// 仅 0 轮：max 停在 0 的旧实现一次都不发。
	p := New()
	p.now = func() time.Time { return t0 }
	_, _ = p.Offer("text", 0, "零轮残尾")
	got := p.FlushAll()
	if len(got) != 1 || got[0].Kind != "text" || got[0].Iteration != 0 || got[0].Text != "零轮残尾" {
		t.Fatalf("0 轮残尾必须被冲刷: %+v", got)
	}

	// 0 轮与 1 轮并存：轮升序 0 → 1，0 轮不被跳过。
	p2 := New()
	p2.now = func() time.Time { return t0 }
	_, _ = p2.Offer("text", 1, "一轮残尾")
	_, _ = p2.Offer("text", 0, "零轮残尾")
	got2 := p2.FlushAll()
	if len(got2) != 2 {
		t.Fatalf("两轮残尾都应被冲刷: %+v", got2)
	}
	if got2[0].Iteration != 0 || got2[1].Iteration != 1 {
		t.Fatalf("顺序应轮升序（0 在前）: %+v", got2)
	}

	// 已收口（Absorb）的轮不参与冲刷。
	p3 := New()
	p3.now = func() time.Time { return t0 }
	_, _ = p3.Offer("text", 0, "会被权威替换")
	p3.Absorb("text", 0)
	_, _ = p3.Offer("text", -1, "负轮残尾")
	got3 := p3.FlushAll()
	if len(got3) != 1 || got3[0].Iteration != -1 {
		t.Fatalf("已收口轮不得再冲刷，未收口轮（含负值）应冲刷: %+v", got3)
	}
}

// TestDeltaFlushAllCoversOpenKinds 词表外 kind 的残尾不得被收口静默丢弃
// （issue #202）：kind 是开放词表（Offer 接受任意串），此前 FlushAll 硬编码
// reasoning/text 两类——宿主用别的 kind 建流时，残尾永久留在 pacer 里
// （Sealed 恒 false），流式观测面的最后一个窗口永不发布。
func TestDeltaFlushAllCoversOpenKinds(t *testing.T) {
	t0 := time.Now()

	// 词表外 kind 单独成流：收口必须冲刷它的残尾。
	p := New()
	p.now = func() time.Time { return t0 }
	_, _ = p.Offer("thinking", 1, "推理残尾")
	got := p.FlushAll()
	if len(got) != 1 || got[0].Kind != "thinking" || got[0].Iteration != 1 || got[0].Text != "推理残尾" {
		t.Fatalf("词表外 kind 的残尾必须被冲刷: %+v", got)
	}
	if again := p.FlushAll(); len(again) != 0 {
		t.Fatalf("已冲刷的残尾不得重复发布: %+v", again)
	}

	// 同轮混开词表：冲刷序仍确定——reasoning→text→其余按名排序。
	p2 := New()
	p2.now = func() time.Time { return t0 }
	_, _ = p2.Offer("tool_delta", 1, "甲")
	_, _ = p2.Offer("text", 1, "乙")
	_, _ = p2.Offer("reasoning", 1, "丙")
	got2 := p2.FlushAll()
	if len(got2) != 3 {
		t.Fatalf("三类残尾都应被冲刷: %+v", got2)
	}
	wantKind := []string{"reasoning", "text", "tool_delta"}
	for i, k := range wantKind {
		if got2[i].Kind != k {
			t.Fatalf("冲刷序应为 reasoning→text→其余按名排序, got %+v", got2)
		}
	}

	// 词表外 kind 之间按名排序：轮升序在前、同类内名升序。
	p3 := New()
	p3.now = func() time.Time { return t0 }
	_, _ = p3.Offer("zeta", 1, "z")
	_, _ = p3.Offer("alpha", 1, "a")
	got3 := p3.FlushAll()
	if len(got3) != 2 || got3[0].Kind != "alpha" || got3[1].Kind != "zeta" {
		t.Fatalf("其余 kind 之间应按名排序: %+v", got3)
	}
}
