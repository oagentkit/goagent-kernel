package memorypipe

import "testing"

// TestBuildIsolatesTagsFromCaller Build 的 Result 与调用方入参不得共享 Tags 的
// 底层数组（issue #394）：Entry 自称「值类型」，而只按值拷贝结构体只复制切片头
// ⇒ 两边同步。标签是 AND 匹配的判据，改穿即匹配结果随之变。
func TestBuildIsolatesTagsFromCaller(t *testing.T) {
	entries := []Entry{{
		ID:     "m1",
		Text:   "合成正文",
		Scope:  "project",
		Tags:   []string{"t1"},
		Source: "s1",
	}}
	res, err := Build(entries, Query{Scope: "project"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(res.Pieces) != 1 {
		t.Fatalf("应注入一条，实得 %d", len(res.Pieces))
	}
	// 方向一：宿主改自己的原变量。
	entries[0].Tags[0] = "mutated"
	if got := res.Pieces[0].Entry.Tags[0]; got != "t1" {
		t.Fatalf("Result 与入参共享了 Tags 底层数组： %+v", res.Pieces[0].Entry.Tags)
	}
	// 方向二：改写结果不得改回入参。
	res.Pieces[0].Entry.Tags[0] = "mutated-out"
	if got := entries[0].Tags[0]; got != "mutated" {
		t.Fatalf("改写结果改回了入参： %+v", entries[0].Tags)
	}
}
