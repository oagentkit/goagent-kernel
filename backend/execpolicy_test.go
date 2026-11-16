package backend

import (
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

// TestJudgeExecRejectsControlSurface 控制面 socket / 命名管道即使写进白名单也拒。
//
// # 为什么这条不再按平台分支
//
// 上游 v0.27.0 的判定用 filepath.Clean + unix 字面量清单：Windows 上 Clean
// 把 unix 路径转成反斜杠形态 ⇒ 清单一条都不命中，判据在该平台**整体空转**
// （本仓 CI 的 windows job 先红过，报「控制面 socket 必须被拒…实得 nil」）。
// 该缺陷已报 oagentkit/guardx#198，上游以「两侧 foldSlash 归一 + 清单补
// Windows 命名管道族」修复（#199）：归一后清单匹配与平台无关。
//
// 用 runtime.GOOS 分支会让「判据在该平台没生效」重新变成不可见——那正是这个
// 缺陷藏过一整版的原因（上游 execpolicy_test 当时也没有平台形态用例）。
// 故三种形态（unix / Windows Clean 形态 / 命名管道）在两个平台上都必须被拒。
func TestJudgeExecRejectsControlSurface(t *testing.T) {
	cases := []struct{ why, source string }{
		{"unix socket", "/var/run/docker.sock"},
		{"Windows Clean 形态（反斜杠）", `\var\run\docker.sock`},
		{"Windows 命名管道", `\\.\pipe\docker_engine`},
	}
	for _, c := range cases {
		j := Job{
			Image:  digestImage,
			Mounts: []Mount{{Source: c.source, Target: "/sock", ReadOnly: true}},
		}
		// 白名单**故意包含**它：判定面必须比配置更严（上游 controlSockets 取向）。
		policy := sandbox.ExecPolicy{MountSourceAllow: []string{c.source}}
		if err := j.JudgeExec(policy); !errors.Is(err, sandbox.ErrMountSourceNotAllowed) {
			t.Fatalf("%s：控制面挂载必须被拒（挂上它等于把容器控制面交出去），实得 %v", c.why, err)
		}
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
