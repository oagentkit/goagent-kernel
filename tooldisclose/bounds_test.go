package tooldisclose

// 目录单条占用上界与占位面长度（issue #290）。
//
// 包注释承诺「目录占用有上界」，但旧实现只截工具描述：参数描述是宿主自由文本，
// schema 全量进目录（50 参 × 5000 字 ≈ 750 KB）。另一面，占位描述在已截到
// MaxDescriptionLen 的描述后再追加取回指令，实际交付 363 rune > 320。
// 两处都必须落成可核对的事实。

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestPlaceholderDescriptionWithinLimit 占位披露面（原文 + 取回指令）不得超过单条上限。
func TestPlaceholderDescriptionWithinLimit(t *testing.T) {
	long := strings.Repeat("描", MaxDescriptionLen*3)
	got := PlaceholderDescription(long)
	if n := utf8.RuneCountInString(got); n > MaxDescriptionLen {
		t.Fatalf("占位描述 %d rune 超过 MaxDescriptionLen=%d", n, MaxDescriptionLen)
	}
	if !strings.HasSuffix(got, PlaceholderNotice) {
		t.Fatal("取回指令必须保留在描述尾部")
	}
	if !strings.Contains(got, "描") {
		t.Fatal("原文前缀应保留（不得只剩指令）")
	}
}

// TestCatalogBoundsParamDescriptions 超长参数描述在构造期截到单参上限。
func TestCatalogBoundsParamDescriptions(t *testing.T) {
	long := strings.Repeat("描", 5000)
	props := make(map[string]Param, 50)
	var required []string
	for i := 0; i < 50; i++ {
		name := "p" + strconv.Itoa(i)
		props[name] = Param{Type: "string", Description: long}
		required = append(required, name)
	}
	c := NewCatalog([]Tool{{Name: "alpha_lookup", Description: "按标识查条目",
		Schema: Schema{Type: "object", Properties: props, Required: required}}})

	got, _ := c.Lookup("alpha_lookup")
	// 50 参 × 5000 字 ≈ 750 KB：目录单条持有量必须落回声明上界。
	if n := schemaBytes(got.Schema); n > MaxSchemaBytes {
		t.Fatalf("schema 内容 %d 字节超过 MaxSchemaBytes=%d", n, MaxSchemaBytes)
	}
	if !got.Schema.Truncated {
		t.Fatal("构造期裁过 schema 必须显式标记 Truncated")
	}
	for name, p := range got.Schema.Properties {
		if n := utf8.RuneCountInString(p.Description); n > MaxParamDescriptionLen {
			t.Fatalf("参数 %s 描述 %d rune 超过 MaxParamDescriptionLen=%d", name, n, MaxParamDescriptionLen)
		}
	}
}

// TestCatalogDropsPropertiesBeyondSchemaBudget 总字节仍超上限时按确定顺序丢弃参数项，
// 并同步过滤 Required；同一输入两次构造必须逐字一致。
func TestCatalogDropsPropertiesBeyondSchemaBudget(t *testing.T) {
	desc := strings.Repeat("描", MaxParamDescriptionLen)
	props := make(map[string]Param, 400)
	var required []string
	for i := 0; i < 400; i++ {
		name := "p" + strconv.Itoa(i)
		props[name] = Param{Type: "string", Description: desc}
		required = append(required, name)
	}
	tool := Tool{Name: "alpha_lookup", Description: "d",
		Schema: Schema{Type: "object", Properties: props, Required: required}}
	c := NewCatalog([]Tool{tool})
	got, _ := c.Lookup("alpha_lookup")

	if n := schemaBytes(got.Schema); n > MaxSchemaBytes {
		t.Fatalf("schema 内容 %d 字节超过 MaxSchemaBytes=%d", n, MaxSchemaBytes)
	}
	if !got.Schema.Truncated {
		t.Fatal("裁掉参数项必须置 Truncated")
	}
	if len(got.Schema.Properties) >= len(props) {
		t.Fatalf("超预算时应丢弃部分参数项，实得 %d/%d", len(got.Schema.Properties), len(props))
	}
	for _, r := range got.Schema.Required {
		if _, ok := got.Schema.Properties[r]; !ok {
			t.Fatalf("Required 里的 %q 已不在 Properties 中", r)
		}
	}
	second, _ := NewCatalog([]Tool{tool}).Lookup("alpha_lookup")
	if !reflect.DeepEqual(got.Schema, second.Schema) {
		t.Fatal("同一输入的裁剪结果必须确定（与 map 遍历序无关）")
	}
}

// TestCatalogSmallSchemaUntouched 小 schema 不受影响：不截断、不丢参数、不误标。
func TestCatalogSmallSchemaUntouched(t *testing.T) {
	c := NewCatalog(tools())
	got, _ := c.Lookup("alpha_lookup")
	if !reflect.DeepEqual(got.Schema, fullSchema()) {
		t.Fatalf("小 schema 不应被改动: %+v", got.Schema)
	}
	if got.Schema.Truncated {
		t.Fatal("未裁剪不应标记 Truncated")
	}
}
