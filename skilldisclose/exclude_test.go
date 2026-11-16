package skilldisclose

// 匹配口径的包内自洽（#291）：Exclude 与技能名走同一条归一化（trim + 小写），
// 资源声明的第三形态（裸名）显式化为「路径段级」匹配而不是任意子串。

import (
	"testing"
)

// TestExcludeIsNormalized Exclude 的三种写法都必须排除。
//
// 声明侧的名字先 TrimSpace 再入库，包内其余匹配全部小写化——Exclude 原值精确比
// 让「Vue」「 vue」「VUE」三种写法全部漏排，重复注入挤占窗口且没有新信息。
//
// 反向验证：去掉 Exclude 归一化（原值作键）⇒ 前三种写法均不生效 ⇒ 本条必红。
func TestExcludeIsNormalized(t *testing.T) {
	c := mustCatalog(t, Skill{Name: "vue", Description: "组件框架指引"})
	for _, variant := range []string{"Vue", " vue", "VUE", "vue", "  VUe  "} {
		res, err := c.Disclose(Query{Mention: "vue", Exclude: []string{variant}}, Budget{})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Skills) != 0 {
			t.Fatalf("Exclude %q 必须排除技能 vue（归一化后同源），实得 %+v", variant, res.Skills)
		}
	}
}

// TestExcludeNormalizedAgainstRegisteredName 排除键与**入库后的名字**同源：
// 声明名带首尾空格时先被 trim 再入库，Exclude 用 trim 前的形态也能排除。
func TestExcludeNormalizedAgainstRegisteredName(t *testing.T) {
	c := mustCatalog(t, Skill{Name: "  vue  ", Description: "组件框架指引"})
	for _, variant := range []string{"vue", " VUE ", "Vue"} {
		res, err := c.Disclose(Query{Mention: "vue", Exclude: []string{variant}}, Budget{})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Skills) != 0 {
			t.Fatalf("Exclude %q 必须对入库名（trim 后）生效，实得 %+v", variant, res.Skills)
		}
	}
}

// TestResourceBareNameMatchesBySegment 裸名声明按**路径段**匹配：
// "log" 命中 "src/log/app.ts" / "log" / "src/log"（段逐字相等），
// **不得**命中 "src/catalog.ts"——"cata**log**" / "b**log**" 里都含 "log"，
// 裸子串匹配把不相关技能当作相关披露（#291）。
//
// 反向验证：把 default 分支改回 strings.Contains ⇒ 第一条必红。
func TestResourceBareNameMatchesBySegment(t *testing.T) {
	c := mustCatalog(t, Skill{Name: "log_kit", Description: "日志处理", Resources: []string{"log"}})

	// 不相关路径不得误命中。
	for _, path := range []string{"src/catalog.ts", "blog/index.md", "slog/entry.log"} {
		res, err := c.Disclose(Query{Resources: []string{path}}, Budget{})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Skills) != 0 {
			t.Fatalf("裸名声明 %q 不得按子串命中 %q（路径段级匹配），实得 %+v", "log", path, res.Skills)
		}
	}
	// 段级命中照常工作：目录段、文件段、整条路径。
	for _, path := range []string{"src/log/app.ts", "log", "log/x.yml", "src/log"} {
		res, err := c.Disclose(Query{Resources: []string{path}}, Budget{})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Skills) != 1 || res.Skills[0].Name != "log_kit" {
			t.Fatalf("路径段逐字相等应命中 %q，实得 %+v", path, res.Skills)
		}
	}
}

// TestResourceDeclaredFormsUnchanged 既有两种声明形态（目录前缀 / 后缀）不受影响：
// 收紧第三形态不得殃及注释里本来就声明的匹配面。
func TestResourceDeclaredFormsUnchanged(t *testing.T) {
	c := mustCatalog(t,
		Skill{Name: "vue_kit", Description: "前端", Resources: []string{".vue"}},
		Skill{Name: "deploy_kit", Description: "部署", Resources: []string{"deploy/"}},
	)
	res, _ := c.Disclose(Query{Resources: []string{"src/App.vue"}}, Budget{})
	if len(res.Skills) != 1 || res.Skills[0].Name != "vue_kit" {
		t.Fatalf("后缀形态必须照常命中，实得 %+v", res.Skills)
	}
	res2, _ := c.Disclose(Query{Resources: []string{"deploy/guide.md"}}, Budget{})
	if len(res2.Skills) != 1 || res2.Skills[0].Name != "deploy_kit" {
		t.Fatalf("目录前缀形态必须照常命中，实得 %+v", res2.Skills)
	}
}
