package main

import (
	"fmt"
	"regexp"
	"strings"
)

// checks.go —— releasecheck 的**纯判据**（不碰 git、不碰文件）。
//
// 分成纯函数是为了让它可判：git 调用是「取事实」，这里才是「事实是否合规」。
// 判据本体若和 exec/git 混在一起，就只能靠造真提交来测——那是集成测试的成本，
// 而这里要的是「坏输入必红」的确定性。

// commit 一条提交与它改过的路径。
type commit struct {
	sha   string
	paths []string
}

// isDepPath 依赖文件：它们只表达「用了哪个版本」，不表达行为。
//
// 刻意只收 go.mod / go.sum：go.work 根本不许入库（modwork 门禁），
// 列进来等于给一条不会出现的路径开后门。
func isDepPath(p string) bool {
	return p == "go.mod" || p == "go.sum"
}

// parseLog 解析 git log 的「控制字符 + sha + 逐行路径」输出。
//
// 为什么用 \x01 而不是解析人类可读的「commit <sha>」行：--name-only 的路径行
// 可能与任何人类可读前缀撞名。控制字符在路径里不可能出现（git 会 quote），
// 故它是安全的分隔符。
//
// 兼容 CRLF：Windows 上 git 输出行尾是 \r\n，不裁掉会让每个路径都带 \r，
// 于是 isDepPath 全部失配、判据整体空转（这类「在某个平台静默失灵」的形态
// 本仓踩过——见 guardx#198）。
func parseLog(raw string) []commit {
	var out []commit
	var cur *commit
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "\x01") {
			out = append(out, commit{sha: strings.TrimSpace(line[1:])})
			cur = &out[len(out)-1]
			continue
		}
		if cur != nil {
			cur.paths = append(cur.paths, line)
		}
	}
	return out
}

// violation 一条混装提交。
type violation struct {
	sha    string
	deps   []string
	others []string
}

func (v violation) String() string {
	return fmt.Sprintf(
		"提交 %s 混装「依赖文件」与「非依赖文件」（deps=%s / others=%s）——"+
			"类型标签只能有一个：chore(deps) 里装了行为变更，它就会落进「依赖地板」段，"+
			"下游扫 breaking 时看不见。拆成两个提交（未发布的提交改写历史是安全的）；"+
			"已发布的登记进 legacyMixed 并写明为什么修不了",
		v.sha, strings.Join(v.deps, ","), strings.Join(v.others, ","))
}

// mixViolations 找出「同时改了依赖文件与非依赖文件」的提交。
//
// legacy 是闭合清单：已发布的历史不可重写，登记它们是为了让门禁在当前 tag 上
// 为绿（否则永远红、然后被忽略——比没有这条规则更糟）。
func mixViolations(commits []commit, legacy map[string]string) []violation {
	var out []violation
	for _, c := range commits {
		var deps, others []string
		for _, p := range c.paths {
			if isDepPath(p) {
				deps = append(deps, p)
			} else {
				others = append(others, p)
			}
		}
		if len(deps) == 0 || len(others) == 0 {
			continue
		}
		if _, ok := legacy[shortSHA(c.sha)]; ok {
			continue
		}
		if _, ok := legacy[c.sha]; ok {
			continue
		}
		out = append(out, violation{sha: c.sha, deps: deps, others: others})
	}
	return out
}

// shortSHA 取 7 位缩写（legacy 清单用缩写登记，人读得懂）。
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// hasSection 判 CHANGELOG 里是否有**本版标题行**「## [vX.Y.Z]」。
//
// 必须锚行首：正文里引用「## [v0.40.0]」这种写法（勘误段就常引用旧版本号）
// 不能被当成本版段——否则门禁会因为一句引用而变绿，正好放过真正的缺失。
func hasSection(md, version string) bool {
	re := regexp.MustCompile("(?m)^## \\[" + regexp.QuoteMeta(version) + "\\]")
	return re.MatchString(md)
}

// missingSections 判 CHANGELOG 的版本段是否覆盖全部 tag（纪律 5）。
//
// 每个 tag 都必须有同名段「## [tag]」——段缺失即返回该 tag（点名缺谁）。
// 只判 tag→段方向（覆盖）：段比 tag 多的「幽灵段」由生成器全量渲染保证不出现
// （gen_changelogs 按全部 tag 切段），出现即说明生成器被绕过，另有产物对账兜。
//
// 为什么必须有这条：v0.74.0 实测——一次合并把 CHANGELOG 从 85 段压到 6 段
// （ce2aa38 只裁决了新旧三段的顺序，没注意到并入分支只剩近段），78 段历史
// 发布说明消失且延续了 10 个版本无人发现（issue #495）。本版段判据（纪律 1）
// 只看本版，历史段整批消失时它照样绿。
func missingSections(tags []string, md string) []string {
	var missing []string
	for _, t := range tags {
		if !hasSection(md, t) {
			missing = append(missing, t)
		}
	}
	return missing
}

// firstReplacementChar 返回首个 U+FFFD 所在行号（1 起）；没有则 bad=false。
func firstReplacementChar(s string) (line int, bad bool) {
	const replacement = "\uFFFD"
	for i, ln := range strings.Split(s, "\n") {
		if strings.Contains(ln, replacement) {
			return i + 1, true
		}
	}
	return 0, false
}
