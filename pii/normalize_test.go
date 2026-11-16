package pii

// #297 判据先于实现：识别面必须有**数字形态归一化**——全角、零宽插入与
// 3-4-4 分组形态此前一律漏检，整串原样外发且 blocked=false。
//
// 缺陷（修复前）：正则只认 ASCII 连续数字形态，Detect 无归一化层——
//
//	Detect("１３８１２３４５６７８")    ⇒ []（全角）
//	Detect("138\u200b12345678")       ⇒ []（零宽插入）
//	Detect("138 1234 5678")           ⇒ []（空格分组）
//
// 修复口径（判据先于实现钉死在这里）：
//
//  1. 归一化与判定**共用一条路径**：匹配在「判定视角」文本上做（全角 ASCII 变体
//     U+FF01–U+FF5E 折半、表意空格折空格、零宽字符剔除），span 映射回原文坐标；
//     语义校验（身份证四重）在归一化后的数字上跑——既有防误命中语义不被拉宽；
//  2. 分组分隔**按误检面评估后定口径**：只认手机号的标准 3-4-4 分组（分隔符
//     单个 `-` / `.` / 空格，含全角形态），且命中之后不得紧跟「分隔符+更多数字」
//     （多组连排是分组列数据，不是单号）——其余分组（4-4-4-4 银行卡等）、
//     身份证分组、其他 Unicode 数字系（阿拉伯-印度文等）一律不认：宁可漏打不可错打；
//  3. 处置面只认原文 span：非连续形态的处置输出为归一化后的 ASCII 形态
//     （保长性质仅在原形态为 ASCII 连续时成立，见 USAGE）。
//
// 反向验证：pii_test.go 的「12 位不打」「13 位毫秒时间戳不打」「19 位数字串」
// 「随机 18 位数字」必须仍绿；本文件再钉归一化自身的防误命中面。

import (
	"strings"
	"testing"
)

func TestNormalizeDetectFullWidthMobile(t *testing.T) {
	restore(t)
	in := "１３８１２３４５６７８"
	spans := Detect(in)
	if len(spans) != 1 || spans[0].Kind != KindMobile {
		t.Fatalf("全角手机号应被检出，实得 %v", spans)
	}
	if spans[0].Start != 0 || spans[0].End != len(in) {
		t.Fatalf("span 应覆盖原文全角形态（字节坐标），实得 %v（len=%d）", spans[0], len(in))
	}
	out, blocked := Apply(SurfaceInput, in)
	if blocked {
		t.Fatal("mask 缺省下不应要求阻断")
	}
	if out != "138****5678" {
		t.Fatalf("全角形态处置输出应为归一化 ASCII 掩码，实得 %q", out)
	}
}

func TestNormalizeDetectFullWidthResidentID(t *testing.T) {
	restore(t)
	valid := withCheck("11010519491231002") // 11010519491231002X
	full := makeFullWidth(valid)
	spans := Detect(full)
	if len(spans) != 1 || spans[0].Kind != KindResidentID {
		t.Fatalf("全角身份证应被检出（四重校验过归一化数字跑），实得 %v", spans)
	}
	out, _ := Apply(SurfaceInput, full)
	// #298 收紧后：留前 4（省市级），生日/顺序码/校验位全掩——校验位不保留。
	if out != "1101**************" {
		t.Fatalf("全角身份证处置输出应为归一化 ASCII 掩码（校验位全掩），实得 %q", out)
	}
}

func TestNormalizeDetectZeroWidthInsertion(t *testing.T) {
	restore(t)
	cases := []string{
		"138\u200b12345678", // ZWSP
		"138\u200c12345678", // ZWNJ
		"138\u200d12345678", // ZWJ
		"138\u206012345678", // WORD JOINER
		"138\ufeff12345678", // BOM
		"１３８\u200b１２３４５６７８", // 全角 + 零宽混合
	}
	for _, in := range cases {
		spans := Detect(in)
		if len(spans) != 1 || spans[0].Kind != KindMobile {
			t.Errorf("零宽插入 %q 应被检出，实得 %v", in, spans)
			continue
		}
		out, _ := Apply(SurfaceInput, in)
		if strings.Contains(out, "12345678") {
			t.Errorf("零宽形态 %q 处置后不得残留原号码，实得 %q", in, out)
		}
		if !strings.Contains(out, "****") {
			t.Errorf("零宽形态 %q 应被处置为掩码，实得 %q", in, out)
		}
	}
}

func TestNormalizeDetectGroupedMobile(t *testing.T) {
	restore(t)
	cases := []string{
		"138 1234 5678",
		"138-1234-5678",
		"138.1234.5678",
		"+86 138 1234 5678",
		"tel:138 1234 5678。", // 符号边界放行
	}
	for _, in := range cases {
		spans := Detect(in)
		if len(spans) != 1 || spans[0].Kind != KindMobile {
			t.Errorf("3-4-4 分组手机号 %q 应被检出，实得 %v", in, spans)
			continue
		}
		out, blocked := Apply(SurfaceInput, in)
		if blocked {
			t.Errorf("%q: mask 缺省下不应要求阻断", in)
		}
		if strings.Contains(out, "1234 5678") || strings.Contains(out, "1234-5678") || strings.Contains(out, "1234.5678") {
			t.Errorf("分组形态 %q 处置后不得残留分组原值，实得 %q", in, out)
		}
		if !strings.Contains(out, "138****5678") {
			t.Errorf("分组形态 %q 处置输出应为归一化掩码，实得 %q", in, out)
		}
	}
}

// TestNormalizeGroupedFalsePositiveSurface 分组形态的误检面口径（定死，防漂移）：
// 非标准分组、多组连排、分隔符叠用、逐位分隔一律不认——宁可漏打不可错打。
func TestNormalizeGroupedFalsePositiveSurface(t *testing.T) {
	restore(t)
	cases := []string{
		"138 1234 5678 9012",    // 3-4-4-4 连排：分组列数据，不认
		"138-1234-5678-9012",    // 同上（连字符）
		"138  1234  5678",       // 分隔符叠用，不认
		"138-1234 5678x",        // 尾随字母，不认
		"138 1234 56789",        // 组内超位（4+1），尾邻数字不认
		"1 2 3 4 5 6 7 8 9 0 1", // 逐位分隔，不认
		"1381 2345 6789",        // 4-4-4（非 3-4-4），不认
		"6222 0202 1234 5678",   // 银行卡 4-4-4-4，不认
		"13812345678",           // 连续形态照常检出（对照组）
	}
	want := map[string]bool{"13812345678": true}
	for _, in := range cases {
		spans := Detect(in)
		if want[in] {
			if len(spans) == 0 {
				t.Errorf("对照组 %q 应被检出", in)
			}
			continue
		}
		if len(spans) != 0 {
			t.Errorf("误检面 %q 不得命中，实得 %v", in, spans)
		}
	}
}

// TestNormalizeDetectBoundaryFullWidth 全角字母邻接与 ASCII 同判：全角标识符
// 内嵌的全角数字串不算边界（与「字母前邻不打」同一条保守纪律）。
func TestNormalizeDetectBoundaryFullWidth(t *testing.T) {
	restore(t)
	cases := []string{
		makeFullWidth("A13812345678"),  // 全角字母前邻
		makeFullWidth("13812345678B"),  // 全角字母后邻
		makeFullWidth("138123456789"),  // 12 位（全角），不打
		makeFullWidth("1328643200000"), // 13 位毫秒时间戳形态（全角），不打
	}
	for _, in := range cases {
		if spans := Detect(in); len(spans) != 0 {
			t.Errorf("%q 不得命中，实得 %v", in, spans)
		}
	}
}

// TestNormalizeResidentIDAntiFalsePositive 全角/零宽形态下身份证的四重校验
// 语义不变：无效证号换一种写法仍然无效。
func TestNormalizeResidentIDAntiFalsePositive(t *testing.T) {
	restore(t)
	cases := []string{
		makeFullWidth("110105194912310021"),           // 校验码错
		makeFullWidth(withCheck("99010519491231002")), // 省码非白名单
		makeFullWidth(withCheck("11010519900230002")), // 生日非法
		makeFullWidth(withCheck("11010520991231002")), // 生日未来
		makeFullWidth(withCheck("11010519491231000")), // 顺序码全零
		"123456789012345678" + "\u200b",               // 随机 18 位 + 零宽尾随
	}
	for _, in := range cases {
		if spans := Detect(in); len(spans) != 0 {
			t.Errorf("%q（无效证号的归一化形态）不得命中，实得 %v", in, spans)
		}
	}
}

// TestNormalizeDigestCrossFormCorrelation hash 档可关联性对**同号异形**成立：
// 同一号码的全角/零宽/分组形态与 ASCII 形态产出同一摘要（归一化与处置共用一条
// 路径，同值同摘要不被形态撕裂）。
func TestNormalizeDigestCrossFormCorrelation(t *testing.T) {
	restore(t)
	if err := SetDigestKey("0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	if err := SetStrategy(StrategyHash); err != nil {
		t.Fatal(err)
	}
	ascii, _ := Apply(SurfaceOutput, "13812345678")
	for _, form := range []string{
		"１３８１２３４５６７８",
		"138\u200b12345678",
		"138 1234 5678",
		"138-1234-5678",
	} {
		d, blocked := Apply(SurfaceOutput, form)
		if blocked {
			t.Fatalf("%q: 已注入密钥不应阻断", form)
		}
		if d != ascii {
			t.Errorf("同号异形应同摘要：%q ⇒ %q，ASCII ⇒ %q", form, d, ascii)
		}
	}
}

// makeFullWidth 把 ASCII 数字与 X/x 折成全角（测试构造用）。
func makeFullWidth(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
			b.WriteRune(r + 0xFEE0)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
