package skilldisclose

// Exclude 归一化与资源声明第三形态（issue #291）。
//
// 同一包内匹配口径必须自洽：名字入库前 TrimSpace、其余匹配全部小写化，但 Exclude
// 原值精确比 ⇒ "Vue"/" vue"/"VUE" 都排除不掉。资源声明的 default 分支是未声明的
// 裸子串匹配 ⇒ 声明 "log" 会命中 "src/catalog.ts"。

import (
	"strings"
	"testing"
)

// TestExcludeNormalizedLikeSkillName Exclude 与技能名走同一条归一化（trim + 小写）。
func TestExcludeNormalizedLikeSkillName(t *testing.T) {
	c := mustCatalog(t, Skill{Name: " Vue ", Description: "处理组件"})
	for _, ex := range []string{"Vue", " vue", "VUE", "  Vue  "} {
		res, err := c.Disclose(Query{Mention: "Vue", Exclude: []string{ex}}, Budget{})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Skills) != 0 {
			t.Fatalf("Exclude=%q 应排除 vue，实得 %+v", ex, res.Skills)
		}
	}
	// 非排除值仍正常披露。
	res, _ := c.Disclose(Query{Mention: "Vue", Exclude: []string{"other"}}, Budget{})
	if len(res.Skills) != 1 {
		t.Fatalf("无关 Exclude 不得影响披露，实得 %+v", res.Skills)
	}
}

// TestResourceBareDeclarationMatchesPathSegmentOnly 裸声明按路径段匹配，不做任意子串。
func TestResourceBareDeclarationMatchesPathSegmentOnly(t *testing.T) {
	c := mustCatalog(t, Skill{Name: "logger", Description: "处理日志", Resources: []string{"log"}})

	miss, _ := c.Disclose(Query{Resources: []string{"src/catalog.ts"}}, Budget{})
	if len(miss.Skills) != 0 {
		t.Fatalf("声明 log 不得命中 catalog.ts，实得 %+v", miss.Skills)
	}

	for _, path := range []string{"log", "src/log/output.txt", "/var/log/app.log"} {
		hit, _ := c.Disclose(Query{Resources: []string{path}}, Budget{})
		if len(hit.Skills) != 1 {
			t.Fatalf("声明 log 应命中路径段 %q，实得 %+v", path, hit.Skills)
		}
	}
}

// TestResourceMultiSegmentDeclaration 裸声明含 / 时按连续路径段匹配。
func TestResourceMultiSegmentDeclaration(t *testing.T) {
	c := mustCatalog(t, Skill{Name: "src_logs", Description: "d", Resources: []string{"src/logs"}})
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"src/logs/app.txt", true},
		{"a/src/logs/app.txt", true},
		{"src/logs", true},
		{"src/logsx/app.txt", false},
		{"other/src/logs/app.txt", true}, // 连续段出现即可
		{"src/other/logs/app.txt", false},
	} {
		res, _ := c.Disclose(Query{Resources: []string{tc.path}}, Budget{})
		if got := len(res.Skills) == 1; got != tc.want {
			t.Fatalf("路径 %q: 命中=%v want %v", tc.path, got, tc.want)
		}
	}
}

// TestResourceFormsDocumented 注释声明的两种形态（目录前缀/后缀）不回退。
func TestResourceFormsDocumented(t *testing.T) {
	c := mustCatalog(t,
		Skill{Name: "vue", Description: "x", Resources: []string{".vue"}},
		Skill{Name: "docs", Description: "y", Resources: []string{"docs/"}},
	)
	res, _ := c.Disclose(Query{Resources: []string{"src/App.vue"}}, Budget{})
	if len(res.Skills) != 1 || res.Skills[0].Name != "vue" {
		t.Fatalf("后缀形态应命中，实得 %+v", res.Skills)
	}
	res2, _ := c.Disclose(Query{Resources: []string{"docs/guide.md"}}, Budget{})
	if len(res2.Skills) != 1 || res2.Skills[0].Name != "docs" {
		t.Fatalf("目录前缀应命中，实得 %+v", res2.Skills)
	}
}

// TestExcludeNormalizedAcrossReason 全部被排除时的空结果原因也要用同一归一口径。
func TestExcludeNormalizedAcrossReason(t *testing.T) {
	c := mustCatalog(t, Skill{Name: "Vue", Description: "d"})
	res, _ := c.Disclose(Query{Mention: "Vue", Exclude: []string{"VUE"}}, Budget{})
	if len(res.Skills) != 0 || !strings.Contains(res.Reason, "Exclude") {
		t.Fatalf("全被排除应有对应原因，实得 %+v", res)
	}
}
