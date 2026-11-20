// kinds_test.go —— 存储 kind 封闭值域的用例。
//
// 夹具（demoMatrix / demoSelection）见 check_test.go（同包共享）。
package main

import (
	"strings"
	"testing"
)

// TestCheckRejectsUnknownStorageKind kind 写了词表外的值 ⇒ 红。
//
// 反向验证：封闭值域的牙在「拼错也红」。只测「合法值能过」的用例，一个把
// 值域判据写成空实现的错实现照样全过。
func TestCheckRejectsUnknownStorageKind(t *testing.T) {
	s := strings.Replace(demoSelection, "    kind: memory\n", "    kind: postgres-x\n", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "只允许")
	joined := strings.Join(probs, "\n")
	if !strings.Contains(joined, "postgres-x") {
		t.Fatalf("违反必须点名写的是什么，实得:\n%s", joined)
	}
}

// TestCheckRejectsMissingStorageKind 没声明 kind ⇒ 红。
//
// 「没写」不是「什么都行」——它是最容易发生的漏登记。
func TestCheckRejectsMissingStorageKind(t *testing.T) {
	s := strings.Replace(demoSelection, "    kind: memory\n", "", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "没有声明 kind")
}

// TestCheckAcceptsEveryKnownStorageKind 词表里的每个取值都能过。
//
// 反向验证的另一半：值域不能收得过窄（收窄会把合法声明面判红 ⇒ 门禁被
// 绕过 ⇒ 没人再信它）。
func TestCheckAcceptsEveryKnownStorageKind(t *testing.T) {
	for _, kind := range storageKinds {
		s := strings.Replace(demoSelection, "    kind: memory\n", "    kind: "+kind+"\n", 1)
		root := writeRoot(t, demoMatrix, s)
		probs, _, err := check(root)
		if err != nil {
			t.Fatalf("判定出错: %v", err)
		}
		for _, p := range probs {
			if strings.Contains(p, "kind") {
				t.Fatalf("合法取值 %q 不该被判红: %s", kind, p)
			}
		}
	}
}

// TestKnownKindClosed 词表本身是封闭的：表外的取值一律不认。
func TestKnownKindClosed(t *testing.T) {
	for _, bad := range []string{"", "file", "postgres", "memory ", "SQLITE"} {
		if knownKind(bad) {
			t.Fatalf("取值 %q 不该被认成合法 kind", bad)
		}
	}
	for _, k := range storageKinds {
		if !knownKind(k) {
			t.Fatalf("词表里的 %q 必须被认成合法 kind", k)
		}
	}
}

// TestKindVocabularyKeepsCollidingSpellings kind 词表里有两个取值**恰好**也是
// 真实存储 id（memory / regional-authority）——同名是**事实**（派单裁决 D 指定的
// 取值集合）⇒ 本用例把「不改取值」钉住：将来谁想「顺手改名消歧」，这条会红。
//
// ⚠ 同名 ≠ 同维度：kind 是**引擎类别**，存储 id 是**声明面里的档位名**。
func TestKindVocabularyKeepsCollidingSpellings(t *testing.T) {
	for _, want := range []string{kindMemory, kindRegionalAuthority} {
		if !knownKind(want) {
			t.Fatalf("取值 %q 必须仍在封闭词表内（与存储 id 同名是事实，不是错）", want)
		}
	}
}

// TestStorageIdAndKindAreDifferentDimensions 存储 id 与 kind 是**两个维度**：
// id 叫 memory 的存储用 sqlite 引擎不判红（门禁不拿 kind 词表去反查 id）。
//
// 这条把「同名不是同维度」从注释变成机器可判的事实：若哪天有人把两者混成一
// 个维度（拿 kind 词表反查存储 id），这条会红。
func TestStorageIdAndKindAreDifferentDimensions(t *testing.T) {
	m := strings.Replace(demoMatrix, "storage: demo-store", "storage: memory", 1)
	s := strings.Replace(demoSelection, "  - id: demo-store\n", "  - id: memory\n", 1)
	s = strings.Replace(s, "    kind: memory\n", "    kind: sqlite\n", 1)
	s = strings.Replace(s, "storages: [demo-store, demo-other]", "storages: [memory, demo-other]", 1)
	root := writeRoot(t, m, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	if len(probs) != 0 {
		t.Fatalf("存储 id 与 kind 是两个维度，不该判红: %v", probs)
	}
}
