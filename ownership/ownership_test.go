// ownership_test.go —— 纯判据：映射配置、提交栅栏、授权→提交的字段投影。
//
// 夹具只带中性词面（region-a / d-1）：夹具是契约的示例，不携带任何部署拓扑。
package ownership

import (
	"testing"

	kerrors "github.com/oekit/pkg/errors"
)

// baseGrant 合成一份当前授权（合法形态；用例按需改单个字段）。
func baseGrant() Grant {
	return Grant{Domain: "d-1", Region: "region-a", Epoch: 7, Token: "own-token-a"}
}

// baseCommit 合成一次提交声明（与 baseGrant 逐字段一致）。
func baseCommit() Commit {
	return baseGrant().Commit()
}

// TestGrantCommitRoundTrip 钉住「授权 → 提交声明」的字段投影：
// 提交路径不得凭记忆手抄代次/令牌（漏一列就是一份可以被旧持有者命中的声明）。
func TestGrantCommitRoundTrip(t *testing.T) {
	g := baseGrant()
	c := g.Commit()
	if c.Domain != g.Domain || c.Region != g.Region || c.Epoch != g.Epoch || c.Token != g.Token {
		t.Fatalf("Commit 必须逐字段投影 Grant：g=%+v c=%+v", g, c)
	}
}

// TestRegionMap 区域权威映射的配置事实：可校验、可查、枚举顺序确定。
func TestRegionMap(t *testing.T) {
	m := RegionMap{"d-2": "region-b", "d-1": "region-a"}
	if err := m.Validate(); err != nil {
		t.Fatalf("合法映射不得报错: %v", err)
	}
	if r, ok := m.Owner("d-1"); !ok || r != "region-a" {
		t.Fatalf("d-1 的权威区域应为 region-a，实得 %q ok=%v", r, ok)
	}
	if _, ok := m.Owner("d-9"); ok {
		t.Fatal("未登记的域不得有权威")
	}
	got := m.Domains()
	if len(got) != 2 || got[0] != "d-1" || got[1] != "d-2" {
		t.Fatalf("Domains 必须按字典序确定，实得 %v", got)
	}
}

// TestRegionMapValidateRejects 配置不合法必须在构造/加载期显式拒绝，
// 不允许「空权威」被当成正常状态（每笔提交都会拿到 ErrUnknownDomain，
// 但配置错误看起来像业务错误）。
func TestRegionMapValidateRejects(t *testing.T) {
	cases := []struct {
		name string
		m    RegionMap
	}{
		{"空映射", RegionMap{}},
		{"空任务域", RegionMap{"": "region-a"}},
		{"空白任务域", RegionMap{"  ": "region-a"}},
		{"空区域", RegionMap{"d-1": ""}},
		{"空白区域", RegionMap{"d-1": " \t"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.m.Validate(); !kerrors.Is(err, ErrBadMapping) {
				t.Fatalf("非法映射必须 ErrBadMapping，实得 %v", err)
			}
		})
	}
}

// TestJudge 纯判据逐项拒绝：判据写反（例如把 epoch 比较写成字符串比较、
// 或先判令牌后判代次）必须在这里红。
func TestJudge(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Grant, *Commit)
		want error
	}{
		{
			name: "一致",
			mut:  func(*Grant, *Commit) {},
			want: nil,
		},
		{
			name: "任务域不一致（授权不是这个域的）",
			mut:  func(_ *Grant, c *Commit) { c.Domain = "d-2" },
			want: ErrUnknownDomain,
		},
		{
			name: "epoch 回退（旧代提交）",
			mut:  func(_ *Grant, c *Commit) { c.Epoch = 6 },
			want: ErrStaleEpoch,
		},
		{
			name: "epoch 超前（未确认的候选代次）",
			mut:  func(_ *Grant, c *Commit) { c.Epoch = 8 },
			want: ErrEpochUnconfirmed,
		},
		{
			name: "区域不是权威区域（代次相同）",
			mut:  func(_ *Grant, c *Commit) { c.Region = "region-b" },
			want: ErrNotOwner,
		},
		{
			name: "令牌不匹配（区域与代次都相同）",
			mut:  func(_ *Grant, c *Commit) { c.Token = "forged" },
			want: ErrTokenMismatch,
		},
		{
			name: "授权令牌为空（坏授权不得放行任何提交）",
			mut:  func(g *Grant, c *Commit) { g.Token = ""; c.Token = "" },
			want: ErrTokenMismatch,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, c := baseGrant(), baseCommit()
			tc.mut(&g, &c)
			err := Judge(g, c)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("期望通过，实得 %v", err)
				}
				return
			}
			if !kerrors.Is(err, tc.want) {
				t.Fatalf("期望 %v，实得 %v", tc.want, err)
			}
		})
	}
}

// TestJudgeChecksEpochBeforeRegion 钉住判定次序：一个旧代、旧区域的提交
// 必须报「代次回退」，不能被报成「区域不是权威区域」——两者的处置不同
// （前者按新代次对账，后者是找错了权威）。
func TestJudgeChecksEpochBeforeRegion(t *testing.T) {
	g := baseGrant()
	c := Commit{Domain: g.Domain, Region: "region-b", Epoch: g.Epoch - 1, Token: "whatever"}
	if err := Judge(g, c); !kerrors.Is(err, ErrStaleEpoch) {
		t.Fatalf("旧代 + 错区域必须优先报 ErrStaleEpoch，实得 %v", err)
	}
	// 未确认代次同样优先于区域判定：目标区域在确认前拿候选代次提交，
	// 报的必须是「未确认」，不是「不是权威区域」——后者会诱导它去查映射。
	c = Commit{Domain: g.Domain, Region: "region-b", Epoch: g.Epoch + 1, Token: "whatever"}
	if err := Judge(g, c); !kerrors.Is(err, ErrEpochUnconfirmed) {
		t.Fatalf("候选代次 + 错区域必须优先报 ErrEpochUnconfirmed，实得 %v", err)
	}
}

// TestJudgeEpochIsNumeric 反向验证：代次比较必须是**数值**比较。
// 把 Epoch 写成字符串比较的实现，在 9 vs 10 这一对上会判反——这类错
// 在单向用例（epoch 1 vs 2）里看不出来。
func TestJudgeEpochIsNumeric(t *testing.T) {
	g := Grant{Domain: "d-1", Region: "region-a", Epoch: 10, Token: "own-token-a"}
	base := g.Commit()
	base.Epoch = 9
	if err := Judge(g, base); !kerrors.Is(err, ErrStaleEpoch) {
		t.Fatalf("epoch 9 对 epoch 10 必须判回退（字符串比较会判反），实得 %v", err)
	}
	base.Epoch = 11
	if err := Judge(g, base); !kerrors.Is(err, ErrEpochUnconfirmed) {
		t.Fatalf("epoch 11 对 epoch 10 必须判未确认（字符串比较会判反），实得 %v", err)
	}
}
