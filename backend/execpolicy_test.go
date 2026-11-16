package backend

import (
	"runtime"
	"testing"
	"time"

	"github.com/oagentkit/guardx/sandbox"
	"github.com/oekit/pkg/errors"
)

// execpolicy_test.go —— 作业声明 ↔ 执行硬约束判据的接缝。
//
// 判据本体（ExecPolicy.Judge）在上游 guardx/sandbox，本仓这层只负责映射。
// 故这里钉两件事：**映射不丢字段**，以及**真实缺口确实被挡住**——这些此前
// Validate 完全看不见（浮动 tag 照样放行、控制面 socket 照样挂）。

// digestImage 一个钉了 digest 的合法镜像引用。
const digestImage = "registry.example/app@sha256:" +
	"0000000000000000000000000000000000000000000000000000000000000001"

// TestExecSpecMapsJobFields 映射不丢字段（丢一个字段 = 那条约束静默失效）。
func TestExecSpecMapsJobFields(t *testing.T) {
	j := Job{
		Image: digestImage,
		Mounts: []Mount{
			{Source: "/data/in", Target: "/work/in", ReadOnly: true},
			{Source: "/data/out", Target: "/work/out"},
		},
		Budget: sandbox.Budget{WallClock: time.Minute, MaxMemory: 64 << 20},
	}
	s := j.ExecSpec()
	if s.Image != j.Image {
		t.Fatalf("镜像没映射过去：%q", s.Image)
	}
	if len(s.Mounts) != 2 {
		t.Fatalf("挂载条数 = %d，want 2", len(s.Mounts))
	}
	if !s.Mounts[0].ReadOnly || s.Mounts[1].ReadOnly {
		t.Fatalf("只读标记丢了一半：%+v", s.Mounts)
	}
	// 映射里**不得**替策略方补「满足」：Job 没有声明的项必须留零值，
	// 否则策略要求只读 rootfs 时会被假装满足。
	if s.RootfsReadOnly || s.NonRoot || len(s.CapDrop) != 0 || s.SeccompProfile != "" {
		t.Fatalf("未声明的约束项不得被默认置真：%+v", s)
	}
	if s.Limits.MaxMemory != 64<<20 {
		t.Fatalf("预算没映射过去：%+v", s.Limits)
	}
}

// TestJudgeExecRejectsFloatingTag 浮动 tag 必须被拒（Validate 放行的那一类）。
func TestJudgeExecRejectsFloatingTag(t *testing.T) {
	j := Job{Image: "registry.example/app:latest"}
	if err := j.Validate(); err != nil {
		t.Fatalf("前提不成立：Validate 本身应放行浮动 tag（这正是缺口所在），实得 %v", err)
	}
	if err := j.JudgeExec(sandbox.ExecPolicy{}); !errors.Is(err, sandbox.ErrImageNotPinned) {
		t.Fatalf("浮动 tag 必须被判 ErrImageNotPinned（否则「钉住什么就跑什么」不成立），实得 %v", err)
	}
}

// TestJudgeExecRejectsControlSocketMount 控制面 socket 即使写进白名单也拒。
//
// # 平台语义（实测踩到，写下来免得下次当 flake 重跑）
//
// 上游 Judge 先用 filepath.Clean 规范化挂载源、再与 controlSockets 清单比对，
// 而那份清单是 unix 路径族。Windows 上 Clean 会把这类路径改成反斜杠形态，
// 于是不命中清单——而该路径族在 Windows 上本就不存在（Docker Desktop 走
// 命名管道），所以这是判据的适用域，不是漏判。
//
// 故本条在 unix 断言「控制面 socket 被拒」；Windows 侧改断言「白名单外的
// 挂载同样被拒」——保证这条判据在两个平台上都真的在判，而不是各自空转。
func TestJudgeExecRejectsControlSocketMount(t *testing.T) {
	source, allowed := "/var/run/docker.sock", "/var/run/docker.sock"
	if runtime.GOOS == "windows" {
		// 用同一路径族（两侧都过同一个 Clean），判据仍必须拒绝。
		source, allowed = "/data/in", "/data/other"
	}
	j := Job{
		Image:  digestImage,
		Mounts: []Mount{{Source: source, Target: "/sock", ReadOnly: true}},
	}
	// 白名单**故意包含**控制面 socket：判定面必须比配置更严（上游取向）。
	policy := sandbox.ExecPolicy{MountSourceAllow: []string{allowed}}
	if err := j.JudgeExec(policy); !errors.Is(err, sandbox.ErrMountSourceNotAllowed) {
		t.Fatalf("控制面 socket / 白名单外挂载必须被拒（挂上控制面等于把容器控制面交出去），实得 %v", err)
	}
}

// TestJudgeExecEnforcesMountReadOnly 策略要求只读时，可写挂载被拒。
func TestJudgeExecEnforcesMountReadOnly(t *testing.T) {
	src := "/data/in"
	policy := sandbox.ExecPolicy{
		MountSourceAllow:  []string{src},
		MountReadOnlyOnly: true,
	}
	rw := Job{Image: digestImage, Mounts: []Mount{{Source: src, Target: "/work/in"}}}
	if err := rw.JudgeExec(policy); !errors.Is(err, sandbox.ErrMountNotReadOnly) {
		t.Fatalf("可写挂载必须被拒为 ErrMountNotReadOnly，实得 %v", err)
	}
	ro := Job{Image: digestImage, Mounts: []Mount{{Source: src, Target: "/work/in", ReadOnly: true}}}
	if err := ro.JudgeExec(policy); err != nil {
		t.Fatalf("只读挂载应通过，实得 %v", err)
	}
}

// TestJudgeExecEnforcesLimits 声明侧上限越过策略值即拒。
func TestJudgeExecEnforcesLimits(t *testing.T) {
	policy := sandbox.ExecPolicy{Limits: sandbox.Budget{MaxMemory: 128 << 20}}
	over := Job{Image: digestImage, Budget: sandbox.Budget{MaxMemory: 256 << 20}}
	if err := over.JudgeExec(policy); !errors.Is(err, sandbox.ErrLimitsExceeded) {
		t.Fatalf("越过策略上限必须被拒为 ErrLimitsExceeded，实得 %v", err)
	}
	within := Job{Image: digestImage, Budget: sandbox.Budget{MaxMemory: 64 << 20}}
	if err := within.JudgeExec(policy); err != nil {
		t.Fatalf("落在策略内应通过，实得 %v", err)
	}
}

// TestJudgeExecPassesPinnedJob 钉住 digest + 白名单内只读挂载 + 预算不越线 = 通过。
// 这一条是「判据有牙但不是全拒」的对照：只测红不测绿，判据可能整个失灵。
func TestJudgeExecPassesPinnedJob(t *testing.T) {
	policy := sandbox.ExecPolicy{
		ImageDigestAllow:  []string{"sha256:0000000000000000000000000000000000000000000000000000000000000001"},
		MountSourceAllow:  []string{"/data/in"},
		MountReadOnlyOnly: true,
	}
	j := Job{
		Image:  digestImage,
		Mounts: []Mount{{Source: "/data/in", Target: "/work/in", ReadOnly: true}},
	}
	if err := j.JudgeExec(policy); err != nil {
		t.Fatalf("合法作业应通过策略判定，实得 %v", err)
	}
}
