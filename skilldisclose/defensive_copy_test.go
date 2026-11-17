package skilldisclose

import "testing"

// TestDiscloseReturnsDefensiveCopy 披露出口必须是**副本**（issue #377）：
// 改写披露结果不得改穿目录——目录是「构造后不再变化」这条不变式的持有者，
// 改穿即破坏它 ⇒ 检索打分与预算裁剪都基于已被外部改写的元数据。
//
// 两条出口都要试：Skills（挑出的那几个）与 Scored（全部候选）。
func TestDiscloseReturnsDefensiveCopy(t *testing.T) {
	c := mustCatalog(t, Skill{
		Name:        "alpha",
		Description: "合成描述",
		Resources:   []string{"src/"},
		Tags:        []string{"t1"},
	})
	res, err := c.Disclose(Query{Mention: "alpha"}, Budget{})
	if err != nil {
		t.Fatalf("Disclose: %v", err)
	}
	if len(res.Skills) == 0 || len(res.Scored) == 0 {
		t.Fatalf("点名必须命中候选： %+v", res)
	}
	// 原地改写披露结果。
	res.Skills[0].Resources[0] = "mutated"
	res.Scored[0].Skill.Tags[0] = "mutated"

	again, err := c.Disclose(Query{Mention: "alpha"}, Budget{})
	if err != nil {
		t.Fatalf("Disclose: %v", err)
	}
	if again.Skills[0].Resources[0] != "src/" {
		t.Fatalf("披露结果改穿回目录（Resources）： %+v", again.Skills[0].Resources)
	}
	if again.Scored[0].Skill.Tags[0] != "t1" {
		t.Fatalf("披露结果改穿回目录（Tags）： %+v", again.Scored[0].Skill.Tags)
	}
}

// TestNewIsolatesFromCaller 构造期必须是**副本**（issue #377）：
// 宿主在 New 之后改自己的原变量不得改穿目录（目录内的两份持有会一起被改，
// 目录自身都不自洽）。
func TestNewIsolatesFromCaller(t *testing.T) {
	skills := []Skill{{
		Name:        "alpha",
		Description: "合成描述",
		Resources:   []string{"src/"},
		Tags:        []string{"t1"},
	}}
	c, err := New(skills)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// 宿主改自己的原变量。
	skills[0].Resources[0] = "mutated"
	skills[0].Tags[0] = "mutated"

	res, err := c.Disclose(Query{Mention: "alpha"}, Budget{})
	if err != nil {
		t.Fatalf("Disclose: %v", err)
	}
	if len(res.Skills) == 0 {
		t.Fatalf("点名必须命中候选： %+v", res)
	}
	if res.Skills[0].Resources[0] != "src/" {
		t.Fatalf("宿主改自己的原变量改穿了目录（Resources）： %+v", res.Skills[0].Resources)
	}
	if res.Scored[0].Skill.Tags[0] != "t1" {
		t.Fatalf("宿主改自己的原变量改穿了目录（Tags）： %+v", res.Scored[0].Skill.Tags)
	}
}
