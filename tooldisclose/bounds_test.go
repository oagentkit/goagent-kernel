package tooldisclose

// schema 侧的占用上界（#290）：「目录占用有上界」要落成可核对的事实，无界点就
// 不能只钉在工具描述上——参数个数与参数自由文本（参数名/类型/描述）同样无界，
// 50 参 × 5000 字的 schema 能让单条目录成员持有数百 KB。占位面（原文+取回指令）
// 也必须守同一条声明上限，不得自己越过。

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestCatalogBoundsSchemaFootprint 注入天文数字 schema ⇒ 目录单条持有量不超过声明上界。
//
// 反向验证：去掉构造期的 boundSchema（schema 原样入库）⇒ 每参 5000 字描述原样
// 进目录 ⇒ 本条必红。
func TestCatalogBoundsSchemaFootprint(t *testing.T) {
	props := make(map[string]Param, MaxParams+16)
	for i := 0; i < MaxParams+16; i++ {
		props["param_"+strconv.Itoa(i)] = Param{
			Type:        strings.Repeat("t", 500),
			Description: strings.Repeat("描", 5000),
		}
	}
	c := NewCatalog([]Tool{{
		Name:        "fat_tool",
		Description: "描述正常",
		Schema:      Schema{Type: "object", Properties: props, Required: []string{"ghost_not_in_props"}},
	}})
	got, ok := c.Lookup("fat_tool")
	if !ok {
		t.Fatal("fat_tool 应在目录内")
	}
	if len(got.Schema.Properties) != MaxParams {
		t.Fatalf("参数个数必须裁到 %d，实得 %d", MaxParams, len(got.Schema.Properties))
	}
	for name, p := range got.Schema.Properties {
		for field, val := range map[string]string{"参数名": name, "类型": p.Type, "描述": p.Description} {
			if n := utf8.RuneCountInString(val); n > MaxParamTextLen {
				t.Fatalf("schema 自由文本字段（%s）超出声明上限 %d，实得 %d", field, MaxParamTextLen, n)
			}
		}
	}
	if len(got.Schema.Required) > MaxParams {
		t.Fatalf("Required 不得多于参数上限，实得 %d", len(got.Schema.Required))
	}
	for _, p := range got.Schema.Required {
		if n := utf8.RuneCountInString(p); n > MaxParamTextLen {
			t.Fatalf("Required 项超出声明上限 %d，实得 %d", MaxParamTextLen, n)
		}
	}
	// 宿主本来声明的悬空 Required 保持原样：那是 SignatureOf 要当场报错的 schema
	// 不自洽，构造期不得悄悄洗掉（既有契约）。
	for _, p := range got.Schema.Required {
		if p == "ghost_not_in_props" {
			return
		}
	}
	t.Fatalf("悬空 Required 不得被构造期洗掉，实得 %v", got.Schema.Required)
}

// TestSchemaBoundTruncatesLongParamNames 参数名超长同样截到声明上限；
// 截断撞名时首次出现处胜出（与目录重名规则同款），数量有界。
func TestSchemaBoundTruncatesLongParamNames(t *testing.T) {
	prefix := "p_" + strings.Repeat("x", 320) // 截断后两者共享同一前缀
	c := NewCatalog([]Tool{{
		Name: "long_names",
		Schema: Schema{
			Type: "object",
			Properties: map[string]Param{
				prefix + "_first":  {Type: "string"},
				prefix + "_second": {Type: "string"},
			},
		},
	}})
	got, _ := c.Lookup("long_names")
	if len(got.Schema.Properties) != 1 {
		t.Fatalf("截断撞名应首次出现处胜出（剩余 1 个参数），实得 %d 个", len(got.Schema.Properties))
	}
	for name := range got.Schema.Properties {
		if n := utf8.RuneCountInString(name); n > MaxParamTextLen {
			t.Fatalf("参数名超出声明上限 %d，实得 %d", MaxParamTextLen, n)
		}
	}
}

// TestSchemaBoundKeepsRequiredFirst 参数超限时的裁剪必须确定且必填优先：
// 必填参数（哪怕名字排最后）全部保留，其余按名升序补足——否则同一份注册表
// 两次构造得到两个目录，或必填被一个可选参数挤掉。
func TestSchemaBoundKeepsRequiredFirst(t *testing.T) {
	props := make(map[string]Param, MaxParams+1)
	for i := 0; i < MaxParams; i++ {
		props["a"+strconv.Itoa(i)] = Param{Type: "string"}
	}
	props["z_last_optional_but_required"] = Param{Type: "string"} // 名字排最后
	c := NewCatalog([]Tool{{
		Name:   "ordered_tool",
		Schema: Schema{Type: "object", Properties: props, Required: []string{"z_last_optional_but_required"}},
	}})
	got, _ := c.Lookup("ordered_tool")
	if len(got.Schema.Properties) != MaxParams {
		t.Fatalf("参数个数应裁到 %d，实得 %d", MaxParams, len(got.Schema.Properties))
	}
	if _, ok := got.Schema.Properties["z_last_optional_but_required"]; !ok {
		t.Fatal("必填参数必须优先保留（哪怕名字排序最后）")
	}
	if len(got.Schema.Required) != 1 || got.Schema.Required[0] != "z_last_optional_but_required" {
		t.Fatalf("Required 应原样保留必填项，实得 %v", got.Schema.Required)
	}
	// 没被裁的 schema 不得被改形：healthy schema 进出逐字段一致。
	for _, keep := range []string{"a0", "a1", "a" + strconv.Itoa(MaxParams-1)} {
		if _, ok := got.Schema.Properties[keep]; !ok {
			t.Fatalf("健康参数 %q 不得被误裁", keep)
		}
	}
}

// TestBoundSchemaLeavesHealthySchemaUntouched 不超限的 schema 原样往返：
// 收界只对越界者生效，健康声明不得被改写（SignatureOf 的派生依赖原字段）。
func TestBoundSchemaLeavesHealthySchemaUntouched(t *testing.T) {
	in := fullSchema()
	out := boundSchema(in)
	if len(out.Properties) != 1 || out.Properties["id"] != in.Properties["id"] {
		t.Fatalf("健康 schema 不得被改写，实得 %+v", out)
	}
	if len(out.Required) != 1 || out.Required[0] != "id" {
		t.Fatalf("Required 应原样保留，实得 %v", out.Required)
	}
}

// TestPlaceholderDescriptionFitsCap 占位披露面（原文 + 取回指令）总长不得超过
// MaxDescriptionLen。#290 实测：旧实现把指令追加到已截断的描述后面，交付面
// 363 rune > 320——声明的单条上限被占位面自己越过。
//
// 反向验证：去掉占位面的长度约束（直接拼接）⇒ 本条必红。
func TestPlaceholderDescriptionFitsCap(t *testing.T) {
	ex := Placeholder("alpha_lookup", strings.Repeat("描", MaxDescriptionLen))
	if n := utf8.RuneCountInString(ex.Description); n > MaxDescriptionLen {
		t.Fatalf("占位披露面 rune 数 %d 超过声明上限 %d（#290）", n, MaxDescriptionLen)
	}
	if !strings.HasSuffix(ex.Description, PlaceholderNotice) {
		t.Fatal("取回指令必须完整保留在尾部（超长只能吃原文尾部）")
	}
	// 短原文不受影响：原文在前、指令在后。
	short := Placeholder("alpha_lookup", "按标识查条目")
	if !strings.Contains(short.Description, "按标识查条目") || !strings.HasSuffix(short.Description, PlaceholderNotice) {
		t.Fatalf("短原文应原样保留并追加指令，实得 %q", short.Description)
	}
	// 目录出口守同一条上限：存储值（已截 320）+ 指令 ≤ 上限。
	c := NewCatalog([]Tool{{Name: "alpha_lookup", Description: strings.Repeat("描", MaxDescriptionLen)}})
	ex2, ok := c.Expose("alpha_lookup")
	if !ok {
		t.Fatal("alpha_lookup 应在目录内")
	}
	if n := utf8.RuneCountInString(ex2.Description); n > MaxDescriptionLen {
		t.Fatalf("目录占位面 rune 数 %d 超过声明上限 %d——两个出口必须共用同一约束", n, MaxDescriptionLen)
	}
}
