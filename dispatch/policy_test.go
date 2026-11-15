package dispatch

import "testing"

// 策略优先级链的契约判据（issue #169）：
// 条目 dispatch > 表级 dispatch_defaults > conf 旋钮 > 代码缺省。

func intPtr(n int) *int    { return &n }
func boolPtr(b bool) *bool { return &b }

// TestResolvePriorityChain 四级优先级逐级覆盖，且**显式声明不被下一级改写**。
func TestResolvePriorityChain(t *testing.T) {
	cases := []struct {
		name      string
		entry     Policy
		defaults  Policy
		conf      Policy
		wantSame  int
		wantCross bool
	}{
		{
			name:      "三层全空 → 代码缺省（同域一次 / 不跨域）",
			wantSame:  DefaultSameDomain,
			wantCross: DefaultCrossDomain,
		},
		{
			name:      "只有 conf 旋钮 → conf 生效",
			conf:      Policy{SameDomain: intPtr(3), CrossDomain: boolPtr(true)},
			wantSame:  3,
			wantCross: true,
		},
		{
			name:      "表级压过 conf",
			defaults:  Policy{SameDomain: intPtr(5)},
			conf:      Policy{SameDomain: intPtr(9), CrossDomain: boolPtr(true)},
			wantSame:  5,
			wantCross: true, // 表级没声明 CrossDomain，继续回落 conf
		},
		{
			name:      "条目压过表级与 conf",
			entry:     Policy{SameDomain: intPtr(2), CrossDomain: boolPtr(true)},
			defaults:  Policy{SameDomain: intPtr(5), CrossDomain: boolPtr(true)},
			conf:      Policy{SameDomain: intPtr(9), CrossDomain: boolPtr(false)},
			wantSame:  2,
			wantCross: true,
		},
		{
			name:      "条目只声明次数 → 跨域字段继续回落表级",
			entry:     Policy{SameDomain: intPtr(4)},
			defaults:  Policy{CrossDomain: boolPtr(true)},
			conf:      Policy{SameDomain: intPtr(9)},
			wantSame:  4,
			wantCross: true,
		},
		{
			name:      "显式 0/false 是声明不是缺省（不被下一级覆盖）",
			entry:     Policy{SameDomain: intPtr(0), CrossDomain: boolPtr(false)},
			defaults:  Policy{SameDomain: intPtr(5), CrossDomain: boolPtr(true)},
			conf:      Policy{SameDomain: intPtr(9), CrossDomain: boolPtr(true)},
			wantSame:  0,
			wantCross: false,
		},
		{
			name:      "负数次数钳成 0（禁用改派）",
			entry:     Policy{SameDomain: intPtr(-7)},
			wantSame:  0,
			wantCross: DefaultCrossDomain,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			same, cross := Resolve(tc.entry, tc.defaults, tc.conf)
			if same != tc.wantSame || cross != tc.wantCross {
				t.Fatalf("Resolve() = (%d, %v), want (%d, %v)",
					same, cross, tc.wantSame, tc.wantCross)
			}
		})
	}
}

// TestEffectiveIsPerField 逐字段独立回落：一层只声明一个字段时，
// 另一个字段必须来自 def（整份覆盖会把未声明字段也吞成零值）。
func TestEffectiveIsPerField(t *testing.T) {
	got := Policy{SameDomain: intPtr(2)}.Effective(Policy{CrossDomain: boolPtr(true)})
	if got.SameDomain == nil || *got.SameDomain != 2 {
		t.Fatalf("SameDomain 应取本层的 2, got %v", got.SameDomain)
	}
	if got.CrossDomain == nil || *got.CrossDomain != true {
		t.Fatalf("CrossDomain 未声明应回落 def 的 true, got %v", got.CrossDomain)
	}
	// 本层显式声明（含 false/0）压过 def。
	got = Policy{SameDomain: intPtr(0), CrossDomain: boolPtr(false)}.
		Effective(Policy{SameDomain: intPtr(5), CrossDomain: boolPtr(true)})
	same, cross := got.Resolved()
	if same != 0 || cross {
		t.Fatalf("显式 0/false 应压过 def, got (%d, %v)", same, cross)
	}
}
