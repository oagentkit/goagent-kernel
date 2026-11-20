// kinds.go —— 存储 kind 的**封闭值域**判据。
//
// 为什么这道牙要紧：kind 此前是**自由标签**——它只出现在门禁的说明行里
// （「说明 存储 %s（%s）声明 %d 项能力」），没有任何值域校验 ⇒ 拼错、乱写、
// 写一个不存在的引擎类别都照样绿 ⇒ 声明面里那一列字没有任何机器保证。
// 读者看到「kind=sqlite」就以为它一定是 SQLite，而实际写的可能是
// 「sqlite-x」「sqllite」或干脆是空的。
//
// 封闭值域 = 判据必须知道「合法值有哪些」⇒ 这五个取值写成**常量**
// （与 writersUnlimited / sourceStorage 同款：取值不许散落成无名字字面量）。
//
// ⚠ 封闭的是**取值集合**，不是存储清单：门禁代码里仍然不许出现任何存储 id
// （那属宿主资产，不是机制）。kind 是**引擎类别**，属机制面的封闭词表——
// 与 evidence 的 kind（只允许 contract）同款，不同处是它不指向某个落点。
package main

import (
	"fmt"
	"strings"
)

// 存储 kind 的封闭词表：引擎类别，不是领域内容。
//
// 五个取值与声明面现状**两侧一致**（memory / sqlite / postgresql /
// shared-authority / regional-authority）——新增一个类别是一次**声明面变更**，
// 必须同时改这里（否则新类别当场红 ⇒ 逼着人走一次有意识的决定，而不是
// 「随手加个字就过了」）。
const (
	kindMemory            = "memory"
	kindSQLite            = "sqlite"
	kindPostgreSQL        = "postgresql"
	kindSharedAuthority   = "shared-authority"
	kindRegionalAuthority = "regional-authority"
)

// storageKinds 允许的 kind 取值（顺序即报告里的列出顺序）。
var storageKinds = []string{
	kindMemory,
	kindSQLite,
	kindPostgreSQL,
	kindSharedAuthority,
	kindRegionalAuthority,
}

// checkStorageKinds 逐条存储核对 kind 取值（封闭值域 ⇒ 未知即红）。
//
// 报红时**点名写的是什么 + 允许哪些取值**：只说「不合法」等于让人自己猜合法
// 值是什么（而合法值正是本判据要维护的东西）。
func checkStorageKinds(sel *Selection) []string {
	var probs []string
	for _, st := range sel.Storages {
		switch {
		case st.Kind == "":
			probs = append(probs, fmt.Sprintf("%s: 存储 %q 没有声明 kind（只允许 %s）",
				selectionPath, st.ID, allowedKinds()))
		case !knownKind(st.Kind):
			probs = append(probs, fmt.Sprintf("%s: 存储 %q 的 kind 写 %q，只允许 %s",
				selectionPath, st.ID, st.Kind, allowedKinds()))
		}
	}
	return probs
}

// knownKind 判 kind 是否落在封闭值域内。
func knownKind(kind string) bool {
	for _, k := range storageKinds {
		if kind == k {
			return true
		}
	}
	return false
}

// allowedKinds 列出允许的取值（报告原文用）。
func allowedKinds() string {
	return strings.Join(storageKinds, " / ")
}
