package gosrc

import (
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

func TestParsePackageSkipsTestFilesAndSubdirs(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("写 %s 失败: %v", name, err)
		}
	}
	write("a.go", "package p\n\nconst A = 1\n")
	write("a_test.go", "package p\n\nconst B = 2\n")
	write("notgo.txt", "忽略我")
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatalf("建子目录失败: %v", err)
	}
	write("sub/c.go", "package sub\n\nconst C = 3\n")

	files, err := ParsePackage(token.NewFileSet(), dir, false)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if _, ok := files["a.go"]; !ok {
		t.Fatalf("a.go 未被收进来：%v", files)
	}
	if _, ok := files["a_test.go"]; ok {
		t.Fatalf("测试文件不得被收进来（词表类判据会把它当生产常量）")
	}
	if _, ok := files["c.go"]; ok {
		t.Fatalf("子目录不得被收进来（调用方扫的是「一个包」）")
	}
	if _, ok := files["notgo.txt"]; ok {
		t.Fatalf("非 .go 文件不得被收进来")
	}
}

func TestParsePackageKeepsCommentsOnDemand(t *testing.T) {
	dir := t.TempDir()
	body := "package p\n\n// Doc 说明。\nconst A = 1\n"
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(body), 0o600); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}

	withComments, err := ParsePackage(token.NewFileSet(), dir, true)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got := withComments["a.go"].Comments; len(got) == 0 {
		t.Fatalf("要注释时必须保留文档注释")
	}

	without, err := ParsePackage(token.NewFileSet(), dir, false)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got := without["a.go"].Comments; len(got) != 0 {
		t.Fatalf("不要注释时不得保留注释")
	}
}

func TestParsePackageReportsUnreadableDir(t *testing.T) {
	if _, err := ParsePackage(token.NewFileSet(), filepath.Join(t.TempDir(), "nope"), false); err == nil {
		t.Fatalf("目录不存在必须报错：判据拿不到源码时必须红，不能静默放过")
	}
}
