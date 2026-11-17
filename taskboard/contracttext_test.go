package taskboard

// contracttext_test.go —— 契约**文本**的机器可判断言
// （issue #360 / #379）。
//
// # 为什么契约文本也要机器判
//
// 哨兵存在 ≠ 契约要求它。issue #360 的根因正是：ErrUnknownState 哨兵在
// 包里、注释也写着「写路径拒绝」，但 Board.Claim 契约的「必须报错」清单
// 里**没有**它——照抄契约的实现不会写这个闸，于是四份参考实现里只有
// 一份拒词表外状态。
//
// ⇒ 把「契约义务」写成**可判的文本断言**：清单里少一条 ⇒ 用例转红。

import (
	"strings"
	"testing"
)

// TestClaimContractMentionsUnknownState Board.Claim 的契约注释必须列出
// ErrUnknownState（读不懂 ⇒ 拒绝认领是契约义务）。
//
// 反向验证：从契约文本删掉 ErrUnknownState ⇒ 本用例必须转红。
func TestClaimContractMentionsUnknownState(t *testing.T) {
	docs := loadPkgDocs(t)
	doc, ok := docs.methods["Board.Claim"]
	if !ok {
		t.Fatal("取不到 Board.Claim 的契约注释（判据按符号取注释）")
	}
	if !strings.Contains(doc, "ErrUnknownState") {
		t.Fatalf("Board.Claim 的契约注释必须列出 ErrUnknownState——"+
			"「读不懂 ⇒ 拒绝认领」是契约义务，不是某个实现的可选加固。实得：\n%s", doc)
	}
	// 同一句里还要点明「不铸令牌」：拒绝认领却铸出令牌，等于把「没领到」
	// 变成「领到了但没人知道」。
	if !strings.Contains(doc, "令牌") {
		t.Fatalf("Board.Claim 的契约注释必须点明拒绝认领时不得铸令牌。实得：\n%s", doc)
	}
}

// TestBoardContractMentionsNoSharedDeps Board 契约必须写明「List / Claim
// 返回的 Task 不得与实现内部共享 Deps 底层数组」（issue #379）。
//
// 反向验证：从契约文本删掉这条 ⇒ 本用例必须转红。
func TestBoardContractMentionsNoSharedDeps(t *testing.T) {
	docs := loadPkgDocs(t)
	doc, ok := docs.types["Board"]
	if !ok {
		t.Fatal("取不到 Board 的契约注释")
	}
	if !strings.Contains(doc, "Deps") || !strings.Contains(doc, "共享") {
		t.Fatalf("Board 的契约注释必须写明返回的 Task 不得与实现内部共享 Deps 底层数组。实得：\n%s", doc)
	}
}

// TestTaskDocDoesNotClaimComparability Task 的类型注释不得宣称「可比较」
// （issue #379）。
//
// 含 slice 的结构体在 Go 里**不可用 == 比较**（语言事实）：写成「可安全
// 复制与比较」的类型注释是**错的声明**，会让宿主照着它写出编译不过的
// 代码，或误以为按值复制是隔离的。
func TestTaskDocDoesNotClaimComparability(t *testing.T) {
	docs := loadPkgDocs(t)
	doc, ok := docs.types["Task"]
	if !ok {
		t.Fatal("取不到 Task 的类型注释")
	}
	// 先剥掉否定表述（「不可比较」是**如实声明**，不算宣称）。
	cleaned := strings.ReplaceAll(doc, "不可比较", "")
	for _, bad := range []string{"可比较", "可以比较", "可安全复制"} {
		if strings.Contains(cleaned, bad) {
			t.Fatalf("Task 的类型注释不得出现「%s」：含 slice 的结构体在 Go 里不可用 == 比较", bad)
		}
	}
	// 如实声明的两点必须都在：共享底层数组的事实 + 不可比较的事实。
	if !strings.Contains(doc, "Deps") {
		t.Fatalf("Task 的类型注释必须点明 Deps 是切片（复制语义的事实来源）。实得：\n%s", doc)
	}
}
