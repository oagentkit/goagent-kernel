package skilldisclose

// 匹配与打分：相关度通道的**唯一**实现面（单技能打分、资源命中、查询分词）。
//
// 与 skilldisclose.go 分文件按职责切：那边是目录、查询与披露判定（挑谁、裁谁、
// 为什么空），这边是「怎么算相关」。打分权重常量留在 skilldisclose.go（它们是
// 判定的量纲），实现只在这里出现一份。

import (
	"strings"
	"unicode"
)

// scoreOf 单个技能的打分（0 = 不命中）。
//
// 入参全是**已经小写化**的形态：它们要么来自目录构造期（entry 上的 lower* 字段），
// 要么来自查询级预计算（mentionLower / pathsLower）。这条口径必须写在签名上而不是
// 靠注释约定——一旦有人在循环里传回原样，结果不会报错，只会**静默漏命中**：
// 查询「Alpha」再也匹配不到描述里的「ALPHA」。
func scoreOf(e entry, mention, mentionLower string, words map[string]bool, pathsLower []string) int {
	s := e.skill
	if mention != "" && mention == s.Name {
		return scoreMention
	}
	score := 0
	if mentionLower != "" && strings.Contains(e.lowerName, mentionLower) {
		score += scoreNameInText
	}
	if words[e.lowerName] {
		score += scoreNameInText
	}
	for _, p := range pathsLower {
		if resourceHitLower(e.lowerResources, p) {
			score += scoreResource
			break
		}
	}
	for _, tag := range e.lowerTags {
		if words[tag] {
			score += scoreTag
			break
		}
	}
	descHits := 0
	for w := range words {
		// len(w) 按字节判：单字节 ASCII 词在这里被滤掉（单字母是噪声）；
		// 非 ASCII 词元（汉字段）每字至少 3 字节，天然越过这道门槛——
		// 单个汉字是有意义的语素，允许它参与描述命中（#288）。
		if w == "" || len(w) < 2 {
			continue
		}
		if strings.Contains(e.lowerDesc, w) {
			descHits++
			if descHits >= scoreDescWordMax {
				break
			}
		}
	}
	return score + descHits
}

// resourceHitLower 判当前资源路径是否落在技能的声明里（两侧入参须已是小写形态）。
//
// 支持两种声明形态：**目录前缀**（以 / 结尾）与**后缀**（以 . 开头）。
// 不做完整 glob：glob 的语义（** 是否跨层）各实现不同，而「按目录/按后缀」
// 这两种已经能表达全部实际用法，且判据无歧义。
//
// 为什么不提供「入参原样、内部小写化」的包装版本：包装层一旦存在，它和主路径就
// 成了两套实现（一套在循环里、一套在循环外），而两边漂移不会报错，只会让某些声明
// 悄悄不再命中。主路径的小写化位置是明确的——声明在构造期（lowerDeclared）、
// 查询路径在查询级（lowerEach）——规则就只剩这一份。
func resourceHitLower(declaredLower []string, pathLower string) bool {
	for _, d := range declaredLower {
		switch {
		case d == "":
			continue
		case strings.HasSuffix(d, "/"):
			if strings.HasPrefix(pathLower, d) {
				return true
			}
		case strings.HasPrefix(d, "."):
			if strings.HasSuffix(pathLower, d) {
				return true
			}
		default:
			if pathLower == d || strings.Contains(pathLower, d) {
				return true
			}
		}
	}
	return false
}

// tokenize 把一段文本切成小写词集。
//
// 分词口径与「披露面以中文为主」对齐（#288）：ASCII 字母数字逐字累积成词，
// 非 ASCII 字母（汉字、假名、带音标的拉丁文等）同样按**连续段**成词——
// 「审查组件的写法」是一个词元，能作为子串命中中文描述。旧口径把全部汉字
// 当分隔符冲掉，中文查询切不出任何词元，整条相关度通道在中文语境下失效。
// 标点与空白（含中文标点——它们不是 unicode 字母）仍是分隔符。
func tokenize(s string) map[string]bool {
	out := map[string]bool{}
	cur := strings.Builder{}
	flush := func() {
		if cur.Len() > 0 {
			out[strings.ToLower(cur.String())] = true
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			cur.WriteRune(r)
		case r > unicode.MaxASCII && unicode.IsLetter(r):
			cur.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return out
}
