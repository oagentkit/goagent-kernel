package pii

// 数字形态归一化（#297）：识别面的**单一口径**。
//
// # 为什么需要这一层
//
// 正则只认 ASCII 连续数字形态，而值形态不归包管：全角输入（中文 IME 的一等
// 形态）、零宽字符插隐（规避检测的常见手法）、3-4-4 分组书写（人读格式）都是
// 同一个 PII 值。缺归一化层时这些形态整串原样外发且 blocked=false——宿主
// 以为「手机号必被打码」，实际什么都没发生。根因是缺归一化层，不是正则写错。
//
// # 归一化与判定共用一条路径
//
// Detect 把原文映射成「判定视角」文本（本文件的 normalizeForm），匹配、边界
// 判定与身份证四重校验全部跑在视角文本上，span 再映射回**原文坐标**——归一化
// 只存在于识别面，处置面拿到的仍是原文切片；防误命中语义（12/13 位不打、
// 边界不邻字母数字、四重校验）因共用同一条路径而不被拉宽。纯 ASCII 输入走
// 直通快路（视角 == 原文，零分配），既有行为逐字节不变。
//
// # 口径边界（写明，防漂移）
//
//   - 折半只覆盖全角 ASCII 变体 U+FF01–U+FF5E（数字、字母、连字符、句点、
//     加号等与 ASCII 一一对应的块）与表意空格 U+3000；其他 Unicode 数字系
//     （阿拉伯-印度文、天城文等）不折——识别面扩张须与误检面纪律一起决策；
//   - 剔除的零宽/不可见字符集合：U+200B、U+200C、U+200D、U+2060、U+FEFF；
//   - 分组分隔只在手机号的标准 3-4-4 形态上认（pii.go 的 groupedMobileRe），
//     分组连排、分隔符叠用、其他分组一律不认——宁可漏打不可错打。

import (
	"unicode/utf8"
)

// zeroWidthForm 零宽/不可见字符（识别面剔除；这些字符不是内容，是形态干扰）。
func zeroWidthForm(r rune) bool {
	return r == 0x200B || r == 0x200C || r == 0x200D || r == 0x2060 || r == 0xFEFF
}

// normalizeForm 把 s 映射为判定视角文本，并返回「视角字节 → 原文字节」的区间
// 映射（与视角文本等长）：starts[i] / ends[i] 是产生视角第 i 个字节的那个 rune
// 在原文中的起止偏移。匹配视角文本 [a,b) 后，原文 span 是
// [starts[a], ends[b-1])——匹配永远落在单字节字符上（正则只认 ASCII 类），
// 多字节原样 rune 的映射项只为对齐索引。
func normalizeForm(s string) (view string, starts, ends []int) {
	var b []byte
	starts = make([]int, 0, len(s))
	ends = make([]int, 0, len(s))
	for i, r := range s {
		end := i + utf8.RuneLen(r)
		var produced int
		switch {
		case r >= 0xFF01 && r <= 0xFF5E:
			b = append(b, byte(r-0xFEE0))
			produced = 1
		case r == 0x3000:
			b = append(b, ' ')
			produced = 1
		case zeroWidthForm(r):
			continue // 剔除：不产生视角字节，也不产生映射项
		default:
			b = append(b, s[i:end]...)
			produced = end - i
		}
		// 映射项按**产出的视角字节**数追加，与 b 的长度严格同步。
		for j := 0; j < produced; j++ {
			starts = append(starts, i)
			ends = append(ends, end)
		}
	}
	return string(b), starts, ends
}

// normalizeText 只要视角文本、不要映射时的轻量形态（处置面取位用）。
func normalizeText(s string) string {
	if asciiOnly(s) {
		return s
	}
	view, _, _ := normalizeForm(s)
	return view
}

// asciiOnly 纯 ASCII 判定：命中即可走零映射快路。
func asciiOnly(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// trailingDigits 从归一化文本尾部收集 want 个数字，返回数字串与它的起始偏移。
// 分组形态里数字夹着分隔符，逐位收——取到的就是末 want 位**数字**本身，与
// 书写形态无关。找不到足够数字时返回 (-1, nil)（形态异常，调用方 fail-closed）。
func trailingDigits(n string, want int) (int, []byte) {
	idx, count := -1, 0
	for i := len(n) - 1; i >= 0; i-- {
		if isASCIIDigit(n[i]) {
			if count++; count == want {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		return -1, nil
	}
	digits := make([]byte, 0, want)
	for i := idx; i < len(n); i++ {
		if isASCIIDigit(n[i]) {
			digits = append(digits, n[i])
		}
	}
	return idx, digits
}

// formMap 视角 → 原文的 span 坐标映射。nil 映射 = 视角即原文（纯 ASCII 快路）。
type formMap struct {
	starts, ends []int
}

// span 把视角文本上的匹配 [a,b) 换算回原文坐标。
func (m *formMap) span(a, b int) (int, int) {
	if m == nil {
		return a, b
	}
	return m.starts[a], m.ends[b-1]
}
