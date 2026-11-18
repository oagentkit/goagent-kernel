// selfcheck_wordtable_test.go —— word_tables 声明面自校验的用例。
//
// # 这些判据守的是「声明面能不能信」
//
// 声明面自相矛盾（包名写错、类别写错、归一出口挂错表、同一张表登记两次、
// 豁免不写理由、不登记不写理由）时，若被放到执行体里判，就会**太晚**
// （要跑到词表门禁那一步才报）而且**太窄**（段一关就没人判）。
// ⇒ 归自校验：恒开、提交前就拦。
//
// 每条「必须红」的用例都是承重的：把自校验改坏，它们必须转红。
package archcheck

import (
	"strings"
	"testing"
)

// wordTableSelfCheckPolicy 造一份带词表登记面的 policy。
func wordTableSelfCheckPolicy(t WordTable, nr []WordTableNotRegistered) *Policy {
	return &Policy{WordTables: &WordTables{Tables: []WordTable{t}, NotRegistered: nr}}
}

// TestWordTableSelfCheckStalePackageIsRed 陈旧包根当天即红。
//
// 包改名/迁走会让这条登记静默失管（连取值都派生不出来），
// 而没有任何别的东西会告诉人。判在自校验这一层（恒开），
// 而不是词表执行体（可以被段开关关掉）。
func TestWordTableSelfCheckStalePackageIsRed(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"other/other.go": "package other\n",
	})
	vs := selfCheckViolations(root, wordTableSelfCheckPolicy(WordTable{
		Pkg: "ghost", Backing: "statusAll", Enum: "AllStatuses",
	}, nil))
	if len(vs) != 1 || vs[0].Rule != selfCheckRuleID {
		t.Fatalf("陈旧包根应报 1 条自校验违反，实得 %v", vs)
	}
	if !strings.Contains(vs[0].Text, "ghost") {
		t.Fatalf("违规文案必须点名陈旧的包名：%q", vs[0].Text)
	}
}

// TestWordTableSelfCheckDuplicateTableIsRed 同一张表登记两次必须红。
//
// 两张登记会各自派生一份取值，而「这张表有哪些取值」只该有一个答案。
func TestWordTableSelfCheckDuplicateTableIsRed(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": wordTableOwnerPkg,
	})
	pol := &Policy{WordTables: &WordTables{Tables: []WordTable{
		{Pkg: "owner", Backing: "statusAll", Enum: "AllStatuses"},
		{Pkg: "owner", Backing: "statusAll", Enum: "AllStatuses"},
	}}}
	vs := selfCheckViolations(root, pol)
	if len(vs) != 1 || vs[0].Rule != selfCheckRuleID {
		t.Fatalf("重复登记应报 1 条自校验违反，实得 %v", vs)
	}
	if !strings.Contains(vs[0].Text, "登记了两次") {
		t.Fatalf("违规文案必须点明是重复登记：%q", vs[0].Text)
	}
}

// TestWordTableSelfCheckNotRegisteredNeedsReason 不登记清单必须写理由。
//
// 不登记也要留痕——否则「没人管」这件事没人记得，等哪天有人登记它时
// 又得重新调研一遍（而实测表明：直接登记会引进几十处假阳）。
func TestWordTableSelfCheckNotRegisteredNeedsReason(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": wordTableOwnerPkg,
	})
	vs := selfCheckViolations(root, wordTableSelfCheckPolicy(WordTable{
		Pkg: "owner", Backing: "statusAll", Enum: "AllStatuses",
	}, []WordTableNotRegistered{{Subject: "protocol.severityAll", Reason: ""}}))
	if len(vs) != 1 || vs[0].Rule != selfCheckRuleID {
		t.Fatalf("不登记清单缺理由应报 1 条自校验违反，实得 %v", vs)
	}
	if !strings.Contains(vs[0].Text, "severityAll") {
		t.Fatalf("违规文案必须点名缺理由的条目：%q", vs[0].Text)
	}
}

// TestWordTableSelfCheckFamilyExcludeNeedsReason 族豁免必须写理由。
//
// 没有理由的豁免等于让那族常量重新变成「没人管的真相」。
func TestWordTableSelfCheckFamilyExcludeNeedsReason(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": wordTableOwnerPkg,
	})
	vs := selfCheckViolations(root, wordTableSelfCheckPolicy(WordTable{
		Pkg: "owner", Backing: "statusAll", Enum: "AllStatuses", Family: FamilyPrefixes{"Status"},
		FamilyExclude: []WordTableFamilyExclude{{Prefix: "StatusOther", Reason: "  "}},
	}, nil))
	// 第十四阶段起 family_exclude 已废弃 ⇒ 同一份声明还会多报一条「已废弃」
	// （两条是**两件事**：缺理由 / 该字段本身已不需要），但「缺理由」这条
	// 仍必须单独出现且点名那个豁免。
	found := false
	for _, v := range vs {
		if v.Rule == selfCheckRuleID &&
			strings.Contains(v.Text, "缺少理由") &&
			strings.Contains(v.Text, "StatusOther") {
			found = true
		}
	}
	if !found {
		t.Fatalf("族豁免缺理由应报自校验违反，实得 %v", vs)
	}
}

// TestWordTableSelfCheckSkipsDisabledSegment 段被关闭时不判它的声明面。
//
// 段缺省即关闭：段都没有了，声明面矛盾也就无从谈起。
func TestWordTableSelfCheckSkipsDisabledSegment(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": wordTableOwnerPkg,
	})
	off := false
	pol := &Policy{WordTables: &WordTables{Enabled: &off, Tables: []WordTable{
		{Pkg: "ghost", Backing: "statusAll", Enum: "AllStatuses"},
	}}}
	vs := selfCheckViolations(root, pol)
	for _, v := range vs {
		if strings.Contains(v.Text, "ghost") {
			t.Fatalf("段被显式关闭时不该判它的声明面：%v", vs)
		}
	}
}
