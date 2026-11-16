package skilldisclose

// 匹配口径：归一化、分词、打分与资源声明匹配。
//
// 单独成文件是为了让「同一包内只有一套匹配口径」可见：小写化位置、Exclude 归一化、
// 资源段级匹配都集中在这里，主流程只调用，不再各写一份。

import (
	"strings"
	"unicode"
)

// lowerEach 逐项小写化（nil 入参原样返回 nil，避免把 nil 变成空切片）。
//
// 供**标签**与**查询侧资源路径**用：原实现对这两者只做 ToLower，不 TrimSpace。
// 「顺手 trim 一下更稳」在这里不是改进而是改语义——带首尾空格的标签因此会从不命中
// 变成命中，而这类改动不会报错，只会悄悄改变披露结果。
func lowerEach(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.ToLower(s)
	}
	return out
}

// lowerDeclared 逐项「TrimSpace + 小写化」。供**技能声明的资源项**用：
// 原实现正是这么规范化的（声明是宿主手写的，允许留空格；查询路径不是）。
func lowerDeclared(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.ToLower(strings.TrimSpace(s))
	}
	return out
}

// normalizeSkillName 技能名/Exclude 的**唯一**归一化口径：TrimSpace + 小写。
//
// 名字在 New 里已 TrimSpace 并单独存了 lowerName；Exclude 是查询侧输入，必须走同一
// 条归一化，否则 "Vue"/" vue"/"VUE" 排除不掉（精确比原值），而包内其余匹配全部
// 大小写无关——两套口径必然漂移（issue #291）。
func normalizeSkillName(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

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
		// len(w) 按字节判：ASCII 单字母因此被跳过；非 ASCII 单字符（如一个汉字）
		// 的 UTF-8 字节数 >= 2，不会被这条门槛误伤。
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

// resourceHit 判当前资源路径是否落在技能的声明里。
//
// **三种声明形态，全部显式**（旧注释只写两种，实际 default 分支是未声明的任意
// 子串匹配——声明 "log" 会命中 "src/catalog.ts"，issue #291）：
//
//   - 目录前缀：以 / 结尾（如 "deploy/"），匹配路径以其开头；
//   - 后缀：以 . 开头（如 ".vue"），匹配路径以其结尾；
//   - 路径段：其余裸声明（如 "log"、"src/logs"），按**完整路径段**匹配——
//     "log" 命中 "src/log/app.log" 但不命中 "src/catalog.ts"；含 / 的声明按
//     连续路径段序列匹配。
//
// 不做完整 glob：glob 的语义（** 是否跨层）各实现不同，而以上三种已能表达
// 全部实际用法，且判据无歧义。段级而非子串是安全/相关性两面共同的要求：
// 子串会把不相关技能（含其资源与标签）注入上下文。
// resourceHitLower 资源匹配的**唯一**判定实现；两侧入参须已是小写形态。
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
			if pathHasSegmentRun(pathLower, d) {
				return true
			}
		}
	}
	return false
}

// pathHasSegmentRun 报告 declared（可含 /）是否作为**连续完整路径段序列**出现在 path 中。
//
// 例："log" 命中 "src/log/app.log"；"src/logs" 命中 "a/src/logs/x" 但不命中
// "src/logsx/x"（"logsx" 不是 "logs"）；"log" 不命中 "catalog.ts"。
func pathHasSegmentRun(path, declared string) bool {
	ds := strings.Split(declared, "/")
	ps := strings.Split(path, "/")
	if len(ds) == 0 || len(ds) > len(ps) {
		return false
	}
	for i := 0; i+len(ds) <= len(ps); i++ {
		match := true
		for j := range ds {
			if ps[i+j] != ds[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// tokenize 把一段文本切成小写词集（按非字母/数字切分）。
//
// 口径与「披露面以中文为主」对齐：unicode.IsLetter/IsDigit 认所有文字系统的
// 字母与数字，连续的汉字因此成为一个可匹配词元；旧实现只认 ASCII，全部汉字被当
// 分隔符冲掉，中文查询整条相关度通道失效（issue #288）。
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
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}
