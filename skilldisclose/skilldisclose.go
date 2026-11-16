// Package skilldisclose 技能披露：**确定性**挑出该给模型看的那几个技能。
//
// 生命周期: Experimental（v0.33.0 定级）——本包仍会改、删、拆，不承诺任何
// 兼容，升级风险自负；定级理由见 api-lifecycle.yaml，分级规则见
// docs/api-compat.md。
//
// # 与 tooldisclose 的分工
//
// @@tooldisclose@ 解决的是「工具太多，一次给全量 schema 撑爆上下文」，它的检索
// 键是**工具调用需求**（我要做什么）。技能不一样：技能是**带资源文件的工作流**，
// 触发信号有三种且强度不同——① 用户/程序**点名**了它；② 描述与当前任务相关；
// ③ 当前要读的文件落在它声明的资源里。把三者混成一个分数就会让「相关」压过
// 「点名」，而那正是用户已经明确表达过的意图。
//
// # 本包只做判定，不做投递
//
// 披露的**内容**（正文注入、文件读取、渐进式加载）归宿主。本包给的是：
// 给定目录与查询，**挑哪几个**、**为什么挑它们**、**有没有被预算裁掉**。
//
// # 三条可机械判定的判据
//
//  1. **点名优先**：命中名字的分数必须高于任何「相关度」信号，且不受描述长度影响。
//  2. **确定性**：同分按名字字典序，结果与目录的遍历顺序、map 顺序无关。
//  3. **裁剪显式**：预算裁掉了候选时必须标出来（@@Result.Truncated@）。
//     静默截断会让宿主以为「就这几个技能相关」，而实际上有更多被挤掉了。
//
// # 空结果不是静默
//
// 没命中时 @@Reason@ 必须写明「为什么是空的」。空结果与「目录里只有一个技能，
// 正好命中」在调用方看起来一样，而前者是「模型不知道有这个能力」的形态——
// 与「技能不存在」不是一回事，宿主据此要不要回退到全量披露。
package skilldisclose

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/oekit/pkg/errors"
)

// Skill 一个技能的**披露元数据**（不含正文：正文归宿主投递）。
type Skill struct {
	// Name 技能名（唯一，模型按它点名）。
	Name string
	// Description 一句话描述（相关度打分用）。
	Description string
	// Resources 该技能能处理的资源路径/后缀（如 "**/*.vue"、"deploy/"）。
	Resources []string
	// Tags 自由标签（宿主可用它做粗筛）。
	Tags []string
}

// Catalog 已登记的技能集合。
type Catalog struct {
	skills []entry
	byName map[string]Skill
}

// entry 目录成员 + 打分会用到的小写形态。
//
// # 为什么把派生形态存下来
//
// 披露是大小写无关的，而打分在**内层循环**里取值：描述要对着每一个查询词判一次包含。
// 现算的写法是 strings.ToLower(s.Description) 写在 `for w := range words` 里——
// 同一份描述被小写化 len(words) 次/技能。而含非 ASCII 的串走 ToLower 的 rune 映射
// 慢路径（unicode.lookupCaseRange），实测 100 技能 × 8 词的中文目录每次披露要
// 1.6–2.4 ms，改造后降到几十微秒。
//
// 目录成员在 New 之后不再变化（没有任何增删入口），故这份派生值算一次即与真相同寿。
// 内存代价：只有**真含大写**的串才多占一份（strings.ToLower 在无字符改变时原样
// 返回入参），中文为主的描述因此与原文共用同一份字节。
type entry struct {
	skill          Skill
	lowerName      string
	lowerDesc      string
	lowerTags      []string
	lowerResources []string
}

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

// New 建目录并做**加载期自检**。
//
// 自检项（任一不满足即报错，不静默去重/取第一个）：
//
//   - 名字非空且唯一（重名会让「点名」指到两个技能，而判定方无从知道是哪个）；
//   - 描述非空（没有描述就无法按相关度打分，它只能靠点名命中——那等于假装登记了）；
//   - 资源项非空串（空串会匹配一切，把一个技能变成万能匹配）。
func New(skills []Skill) (*Catalog, error) {
	c := &Catalog{byName: make(map[string]Skill, len(skills))}
	for _, s := range skills {
		name := strings.TrimSpace(s.Name)
		if name == "" {
			return nil, errors.New("skilldisclose: 技能名为空（点名与留痕都无从定位）")
		}
		if _, ok := c.byName[name]; ok {
			return nil, fmt.Errorf("skilldisclose: 技能名 %q 重复（点名会指到两个技能）", name)
		}
		if strings.TrimSpace(s.Description) == "" {
			return nil, fmt.Errorf("skilldisclose: 技能 %q 没有描述（只能靠点名命中，等于假装登记了）", name)
		}
		for _, r := range s.Resources {
			if strings.TrimSpace(r) == "" {
				return nil, fmt.Errorf("skilldisclose: 技能 %q 有空的资源项（空串会匹配一切）", name)
			}
		}
		s.Name = name
		c.skills = append(c.skills, entry{
			skill:          s,
			lowerName:      strings.ToLower(name),
			lowerDesc:      strings.ToLower(s.Description),
			lowerTags:      lowerEach(s.Tags),
			lowerResources: lowerDeclared(s.Resources),
		})
		c.byName[name] = s
	}
	slices.SortFunc(c.skills, func(a, b entry) int { return strings.Compare(a.skill.Name, b.skill.Name) })
	return c, nil
}

// Query 一次披露请求。
type Query struct {
	// Mention 点名（用户/程序明确提到的技能名，可空）。
	Mention string
	// Text 当前任务的自然语言描述（相关度打分用）。
	Text string
	// Resources 当前上下文里涉及的文件路径（资源匹配用）。
	Resources []string
	// Exclude 已披露过的技能（**不重复披露**：重复注入会挤占窗口且没有新信息）。
	Exclude []string
}

// Budget 披露预算。
type Budget struct {
	// Max 最多披露几个（<=0 表示不限制数量）。
	Max int
	// MaxBytes 元数据字节上限（<=0 表示不限制）。
	MaxBytes int
}

// Scored 一个候选及其分数（留着让宿主能解释「为什么是它」）。
type Scored struct {
	Skill Skill
	Score int
}

// Result 披露结果。
type Result struct {
	// Skills 按分数降序（同分按名字）挑出的技能。
	Skills []Skill
	// Scored 全部候选的完整打分（含未入选者，留痕用）。
	Scored []Scored
	// Truncated 是否因预算裁掉了候选。
	Truncated bool
	// Reason 空结果或裁剪的原因（人读；空结果时必须写）。
	Reason string
}

// 打分权重。点名必须压过一切相关度信号：用户已经明确表达过的意图不该被
// 「描述里有几个词相同」盖过去。
const (
	scoreMention     = 1000
	scoreNameInText  = 100
	scoreResource    = 30
	scoreTag         = 20
	scoreDescWordMax = 5
)

// Disclose 按查询挑技能。
func (c *Catalog) Disclose(q Query, b Budget) (Result, error) {
	excluded := make(map[string]bool, len(q.Exclude))
	for _, e := range q.Exclude {
		excluded[e] = true
	}
	words := tokenize(q.Text)
	mention := strings.TrimSpace(q.Mention)
	// 点名词与资源路径都是**查询级**的不变量：小写化一次，别在技能循环里重复。
	mentionLower := strings.ToLower(mention)
	pathsLower := lowerEach(q.Resources)

	var scored []Scored
	for _, e := range c.skills {
		if excluded[e.skill.Name] {
			continue
		}
		score := scoreOf(e, mention, mentionLower, words, pathsLower)
		if score == 0 {
			continue
		}
		scored = append(scored, Scored{Skill: e.skill, Score: score})
	}
	slices.SortStableFunc(scored, func(a, b Scored) int {
		if c := cmp.Compare(b.Score, a.Score); c != 0 {
			return c
		}
		return strings.Compare(a.Skill.Name, b.Skill.Name)
	})

	res := Result{Scored: scored}
	if len(scored) == 0 {
		if mention == "" && len(words) == 0 && len(pathsLower) == 0 {
			// 两种空结局必须分开说（#288）：「没有携带任何相关度信号」是
			// 「无从打分」——宿主该补全查询（点名或任务关键词）或直接回退
			// 全量披露；「有信号但没命中」才可能解读成「目录里确无此技能」。
			// 共用一句话时，宿主对无信号查询会误当成「确无此技能」。
			res.Reason = "查询没有携带任何相关度信号（未点名、检索词切不出词元、也没给资源路径）——" +
				"本包无从打分；宿主应补全查询（点名或任务关键词），或直接回退到全量披露"
			return res, nil
		}
		res.Reason = "没有技能命中本次查询（点名/描述/资源三项都没匹配）——" +
			"宿主应据此判断要不要回退到全量披露，而不是当成「没有这个能力」"
		return res, nil
	}

	picked := scored
	if b.Max > 0 && len(picked) > b.Max {
		picked = picked[:b.Max]
		res.Truncated = true
		res.Reason = "候选数超过 Max"
	}
	if b.MaxBytes > 0 {
		var kept []Scored
		used := 0
		for _, sc := range picked {
			n := len(sc.Skill.Name) + len(sc.Skill.Description)
			if used+n > b.MaxBytes {
				res.Truncated = true
				if res.Reason == "" {
					res.Reason = "元数据字节数超过 MaxBytes"
				}
				break
			}
			used += n
			kept = append(kept, sc)
		}
		picked = kept
		if len(picked) == 0 {
			res.Reason = "预算连一条元数据都放不下（MaxBytes 太小）"
		}
	}
	for _, sc := range picked {
		res.Skills = append(res.Skills, sc.Skill)
	}
	return res, nil
}

// scoreOf / resourceHitLower / tokenize 见 match.go：匹配与打分的唯一实现面
// 集中在那边（本文件只留目录、查询与披露判定）。
