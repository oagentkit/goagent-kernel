package protocol

// 脚手架标记词表的单源与机器可读导出（issue #285）。
//
// 词表原先在本仓是包内私有 var，宿主组装侧与前端各自手抄一份——「新增标记时三处
// 同步」只是散文，没有任何判据会在漂移时变红。现在清单文件是唯一来源：
// report_sanitize.go 嵌入它、ScaffoldMarkers() 导出它、宿主与前端消费同一份文件。

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// TestScaffoldMarkersDriveSanitizer 导出的每个标记都必须真的被净化面识别。
//
// 这是「清单 ↔ 净化词表不一致会红」的判据：净化面若退回自带一份硬编码词表，
// 清单里新增的标记会在这里立刻暴露。
func TestScaffoldMarkersDriveSanitizer(t *testing.T) {
	markers := ScaffoldMarkers()
	if len(markers) == 0 {
		t.Fatal("脚手架标记清单不得为空")
	}
	seen := map[string]bool{}
	for _, m := range markers {
		if strings.TrimSpace(m) == "" {
			t.Fatal("标记不得为空串")
		}
		if seen[m] {
			t.Fatalf("标记 %q 重复", m)
		}
		seen[m] = true
		for _, in := range []string{"正文。\n\n" + m + "\n载荷", "正文。" + m + "载荷"} {
			if got := sanitizeScaffoldText(in); strings.Contains(got, m) {
				t.Fatalf("标记 %q 未被净化: %q", m, got)
			}
		}
	}
	// 事故形态的最小快照：这三个标记曾在实弹里复述进报告字段。
	// 新增标记只需改清单；删除/改名这三个之一必须让本用例变红。
	for _, must := range []string{"【历史对话】", "【上一轮系统结论】", "【调度上下文】"} {
		if !seen[must] {
			t.Fatalf("清单缺少已知事故标记 %q", must)
		}
	}
}

// TestScaffoldMarkersManifestMatchesExport 机器可读清单与导出函数必须逐字一致。
func TestScaffoldMarkersManifestMatchesExport(t *testing.T) {
	raw, err := os.ReadFile("scaffold_markers.json")
	if err != nil {
		t.Fatalf("清单文件应随包提供: %v", err)
	}
	var fromFile []string
	if err := json.Unmarshal(raw, &fromFile); err != nil {
		t.Fatalf("清单文件应是合法 JSON: %v", err)
	}
	if got := ScaffoldMarkers(); !reflect.DeepEqual(got, fromFile) {
		t.Fatalf("导出词表与清单文件不一致:\n export=%v\n  file=%v", got, fromFile)
	}
}

// TestScaffoldMarkersReturnsCopy 导出的是副本：调用方改返回值不得改穿净化面。
func TestScaffoldMarkersReturnsCopy(t *testing.T) {
	got := ScaffoldMarkers()
	if len(got) == 0 {
		t.Fatal("词表不得为空")
	}
	got[0] = "【被调用方改掉】"
	if ScaffoldMarkers()[0] == "【被调用方改掉】" {
		t.Fatal("ScaffoldMarkers 必须返回副本")
	}
}
