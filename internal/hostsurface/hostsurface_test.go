// hostsurface_test.go —— Judge 纯函数的可执行判据（数据一律合成，不读盘不联网）。
package hostsurface

import (
	"strings"
	"testing"
)

// validRegistry 合成一份全绿登记面：六步 + 三个包（覆盖基准面与之相等）。
func validRegistry() *Registry {
	return &Registry{
		Updated: "2026-11-28",
		Steps: []StepRow{
			{ID: "create", Name: "创建", HostProvides: "任务拓扑与幂等键", KernelDelivers: "SeedRun 落账"},
			{ID: "claim", Name: "领取", HostProvides: "worker 装配", KernelDelivers: "租约认领"},
			{ID: "execute", Name: "执行", HostProvides: "工具实现", KernelDelivers: "受管执行"},
			{ID: "commit", Name: "提交", HostProvides: "存储实现", KernelDelivers: "闸与栅栏"},
			{ID: "recover", Name: "恢复", HostProvides: "重启入口", KernelDelivers: "接管重放"},
			{ID: "intervene", Name: "介入", HostProvides: "审批回调", KernelDelivers: "权威裁决"},
		},
		Packages: []PackageRow{
			{Pkg: "contract", Class: ClassRequired, Duty: "编排运行契约"},
			{Pkg: "dispatch", Class: ClassOptional, Duty: "改派兜底"},
			{Pkg: "internal/routeledger", Class: ClassInternal, Duty: "路线对账判据"},
		},
	}
}

// validFacts 与登记面配套的仓内事实。
func validFacts() Facts {
	return Facts{
		RegisteredPackages: map[string]bool{
			"contract": true, "dispatch": true, "internal/routeledger": true,
		},
		DocImports: map[string]bool{"contract": true},
	}
}

func hasProblem(problems []string, sub string) bool {
	for _, p := range problems {
		if strings.Contains(p, sub) {
			return true
		}
	}
	return false
}

// TestJudgeHappyPath 全绿登记面：零问题、计数正确。
func TestJudgeHappyPath(t *testing.T) {
	res := Judge(validRegistry(), validFacts())
	if len(res.Problems) != 0 {
		t.Fatalf("全绿登记面不该有问题，实得：%v", res.Problems)
	}
	if res.Steps != 6 || res.Packages != 3 || res.Imports != 1 {
		t.Fatalf("计数应为 6/3/1，实得 %d/%d/%d", res.Steps, res.Packages, res.Imports)
	}
}

// TestJudgeNilInputs 判据跑不起来时必须说话。
func TestJudgeNilInputs(t *testing.T) {
	if res := Judge(nil, validFacts()); !hasProblem(res.Problems, "跑不起来") {
		t.Fatalf("nil 登记面应报「跑不起来」，实得：%v", res.Problems)
	}
	f := validFacts()
	f.RegisteredPackages = nil
	if res := Judge(validRegistry(), f); !hasProblem(res.Problems, "跑不起来") {
		t.Fatalf("nil 包全集应报「跑不起来」，实得：%v", res.Problems)
	}
}

// TestJudgeMissingPackage 漏登记一个包 ⇒ 红并点名（那个包对宿主视角隐形）。
func TestJudgeMissingPackage(t *testing.T) {
	reg := validRegistry()
	f := validFacts()
	f.RegisteredPackages["worker"] = true // 基准面多一个没分类的包
	res := Judge(reg, f)
	if !hasProblem(res.Problems, "worker 没有接入面分类") {
		t.Fatalf("漏分类必须点名，实得：%v", res.Problems)
	}
}

// TestJudgeUnknownPackage 分类表自造包名 ⇒ 红（分类必须挂在生命周期清单上）。
func TestJudgeUnknownPackage(t *testing.T) {
	reg := validRegistry()
	reg.Packages = append(reg.Packages, PackageRow{Pkg: "notexist", Class: ClassOptional, Duty: "x"})
	res := Judge(reg, validFacts())
	if !hasProblem(res.Problems, "notexist") {
		t.Fatalf("自造包名必须点名，实得：%v", res.Problems)
	}
}

// TestJudgeBadClass 分类值域外 ⇒ 红。
func TestJudgeBadClass(t *testing.T) {
	reg := validRegistry()
	reg.Packages[1].Class = "半公开"
	res := Judge(reg, validFacts())
	if !hasProblem(res.Problems, "半公开") {
		t.Fatalf("值域外分类必须点名，实得：%v", res.Problems)
	}
}

// TestJudgeEmptyDuty 职责格留白 ⇒ 红。
func TestJudgeEmptyDuty(t *testing.T) {
	reg := validRegistry()
	reg.Packages[0].Duty = " "
	res := Judge(reg, validFacts())
	if !hasProblem(res.Problems, "contract") {
		t.Fatalf("职责留白必须点名该包，实得：%v", res.Problems)
	}
}

// TestJudgeMissingStep 六步缺一步 ⇒ 红并点名。
func TestJudgeMissingStep(t *testing.T) {
	reg := validRegistry()
	reg.Steps = reg.Steps[:5]
	res := Judge(reg, validFacts())
	if !hasProblem(res.Problems, "intervene") {
		t.Fatalf("缺步必须点名 intervene，实得：%v", res.Problems)
	}
}

// TestJudgeStepHalfEmpty 「宿主提供」留白 ⇒ 红（对照表只剩 kernel 自说自话）。
func TestJudgeStepHalfEmpty(t *testing.T) {
	reg := validRegistry()
	reg.Steps[0].HostProvides = ""
	res := Judge(reg, validFacts())
	if !hasProblem(res.Problems, "create") {
		t.Fatalf("宿主半边留白必须点名 create，实得：%v", res.Problems)
	}
}

// TestJudgeInternalImportDoc 文档 import 内部件 ⇒ 红（把内部实现当公共 API 卖）。
func TestJudgeInternalImportDoc(t *testing.T) {
	f := validFacts()
	f.DocImports["internal/routeledger"] = true
	res := Judge(validRegistry(), f)
	if !hasProblem(res.Problems, "内部件 internal/routeledger") {
		t.Fatalf("文档 import 内部件必须报，实得：%v", res.Problems)
	}
}

// TestJudgeUnregisteredImportDoc 文档 import 未登记包 ⇒ 红（先登记再教）。
func TestJudgeUnregisteredImportDoc(t *testing.T) {
	f := validFacts()
	f.DocImports["ghostpkg"] = true
	res := Judge(validRegistry(), f)
	if !hasProblem(res.Problems, "ghostpkg") {
		t.Fatalf("未登记包 import 必须报，实得：%v", res.Problems)
	}
}

// TestJudgeOptionalImportAllowed 文档 import 可选包 ⇒ 绿（白名单口径是必须接+可选）。
func TestJudgeOptionalImportAllowed(t *testing.T) {
	f := validFacts()
	f.DocImports["dispatch"] = true
	res := Judge(validRegistry(), f)
	if len(res.Problems) != 0 {
		t.Fatalf("可选包 import 不该报，实得：%v", res.Problems)
	}
}
