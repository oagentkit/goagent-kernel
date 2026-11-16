package tooldisclose

// 目录的「构造后不可变」不变式（issue #316）。
//
// Catalog 用 RWMutex 保护 tools，但 Schema.Properties(map) / Required(slice) 是引用类型。
// 构造期与出口只要有任何一处直接交出内部引用，调用方在锁外改写就改穿目录：
// 投给模型的工具契约被静默篡改，并发下是真实数据竞争。所有出口必须交出副本。

import (
	"reflect"
	"sync"
	"testing"
)

func immutableSchema() Schema {
	return Schema{
		Type: "object",
		Properties: map[string]Param{
			"id":   {Type: "string", Description: "标识"},
			"path": {Type: "string", Description: "路径"},
		},
		Required: []string{"id"},
	}
}

func assertSchemaIntact(t *testing.T, got Tool, where string) {
	t.Helper()
	want := immutableSchema()
	if !reflect.DeepEqual(got.Schema, want) {
		t.Fatalf("%s 后目录内部 schema 被改穿:\n got %+v\nwant %+v", where, got.Schema, want)
	}
}

// TestCatalogExitsReturnCopies 五条出口（Lookup/Load/Expose/ExposeAll/Search）
// 的返回值原地改写后，再次读取必须逐字不变。
func TestCatalogExitsReturnCopies(t *testing.T) {
	c := NewCatalog([]Tool{{Name: "alpha_lookup", Description: "按标识查条目", Schema: immutableSchema()}})
	c.Load("alpha_lookup")

	mutate := func(s *Schema, tag string) {
		s.Properties["id"] = Param{Type: "number", Description: tag}
		s.Properties["extra"] = Param{Type: "string"}
		delete(s.Properties, "path")
		if len(s.Required) > 0 {
			s.Required[0] = tag
		}
		s.Required = append(s.Required, "extra")
	}

	// [1] Lookup
	tool, ok := c.Lookup("alpha_lookup")
	if !ok {
		t.Fatal("alpha_lookup 应在目录内")
	}
	mutate(&tool.Schema, "MUTATED-BY-LOOKUP")
	if again, _ := c.Lookup("alpha_lookup"); !reflect.DeepEqual(again.Schema, immutableSchema()) {
		t.Fatalf("Lookup 返回值改穿了目录: %+v", again.Schema)
	}

	// [2] Load
	tool, _ = c.Load("alpha_lookup")
	mutate(&tool.Schema, "MUTATED-BY-LOAD")
	assertSchemaIntact(t, mustLookup(t, c, "alpha_lookup"), "Load")

	// [3] Expose（已加载给完整 schema）
	ex, ok := c.Expose("alpha_lookup")
	if !ok {
		t.Fatal("alpha_lookup 应有披露面")
	}
	mutate(&ex.Schema, "MUTATED-BY-EXPOSE")
	assertSchemaIntact(t, mustLookup(t, c, "alpha_lookup"), "Expose")

	// [4] ExposeAll
	all := c.ExposeAll()
	if len(all) != 1 {
		t.Fatalf("目录应有一项，实得 %d", len(all))
	}
	mutate(&all[0].Schema, "MUTATED-BY-EXPOSEALL")
	assertSchemaIntact(t, mustLookup(t, c, "alpha_lookup"), "ExposeAll")

	// [5] Search（Match 内嵌完整 Tool）
	ms := c.Search("alpha_lookup")
	if len(ms) != 1 {
		t.Fatalf("应命中一项，实得 %d", len(ms))
	}
	mutate(&ms[0].Schema, "MUTATED-BY-SEARCH")
	assertSchemaIntact(t, mustLookup(t, c, "alpha_lookup"), "Search")
}

// TestCatalogCopiesHostInput 构造后改宿主传入的原 map/slice，目录内部必须不变。
func TestCatalogCopiesHostInput(t *testing.T) {
	ts := []Tool{{Name: "alpha_lookup", Description: "按标识查条目", Schema: immutableSchema()}}
	c := NewCatalog(ts)

	ts[0].Schema.Properties["id"] = Param{Type: "number", Description: "MUTATED-BY-HOST"}
	delete(ts[0].Schema.Properties, "path")
	ts[0].Schema.Required[0] = "MUTATED-BY-HOST"
	ts[0].Schema.Required = append(ts[0].Schema.Required, "extra")

	assertSchemaIntact(t, mustLookup(t, c, "alpha_lookup"), "构造后改宿主入参")
}

// TestCatalogConcurrentReturnedMutation 并发「读目录」+「改返回值」不得产生锁外写穿。
//
// 这条在 -race 下才有完整信号；非 race 模式也会在最终一致性断言上暴露明显改穿。
func TestCatalogConcurrentReturnedMutation(t *testing.T) {
	c := NewCatalog([]Tool{{Name: "alpha_lookup", Description: "按标识查条目", Schema: immutableSchema()}})
	c.Load("alpha_lookup")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(tag int) {
			defer wg.Done()
			tool, _ := c.Lookup("alpha_lookup")
			if tool.Schema.Properties != nil {
				tool.Schema.Properties["id"] = Param{Description: "mutated"}
			}
			tool.Schema.Required = append(tool.Schema.Required, "extra")
			_ = c.Search("alpha_lookup")
			_ = c.ExposeAll()
		}(i)
	}
	wg.Wait()

	if got, _ := c.Lookup("alpha_lookup"); !reflect.DeepEqual(got.Schema, immutableSchema()) {
		t.Fatalf("并发改写返回值后目录内部变了: %+v", got.Schema)
	}
}

// TestCatalogCopiesNilShapes nil 形态不应被拷贝成非 nil（退化 schema 的可判性依赖它）。
func TestCatalogCopiesNilShapes(t *testing.T) {
	c := NewCatalog([]Tool{{Name: "alpha_lookup", Description: "d"}})
	got, _ := c.Lookup("alpha_lookup")
	if got.Schema.Properties != nil || got.Schema.Required != nil {
		t.Fatalf("nil map/slice 应保持 nil，实得 %+v", got.Schema)
	}
}

func mustLookup(t *testing.T, c *Catalog, name string) Tool {
	t.Helper()
	tool, ok := c.Lookup(name)
	if !ok {
		t.Fatalf("%s 应在目录内", name)
	}
	return tool
}
