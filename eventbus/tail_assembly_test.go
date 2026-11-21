package eventbus

// event-tail 跨副本补灌面的**装配校验**契约面（issue #166）：
// 装配点 fail fast——nil 存储/Hub 直接 panic（半成品装配只会空转，
// 不留到运行时）。
//
// 本文件由 eventbus/tail_test.go 按关注点拆出（#443：原文件 379/400 行，逼近
// 结构门禁 max_file_lines）。夹具与共享助手在 tail_fixture_test.go，只此一份。

import "testing"

// TestNewTailerRejectsNilDeps 装配点 fail fast：nil 存储/Hub 直接 panic
// （半成品装配只会空转，不留到运行时）。
func TestNewTailerRejectsNilDeps(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("nil store 应 panic")
		}
	}()
	NewTailer(nil, NewHub())
}
