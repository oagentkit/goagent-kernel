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
	//
	// 计量口径是**实际交付的元数据**（MetadataBytes：Name + Description +
	// Tags + Resources），不是只算 name+description：宿主拿到的就是整个 Skill，
	// 计费口径与交付口径必须是同一份（issue #289）。
	MaxBytes int
}

// MetadataBytes 一条技能作为披露元数据实际占用的字节数：Name + Description +
// 全部 Tags + 全部 Resources（按 UTF-8 字节计）。
//
// 为什么不含分隔符/容器开销：这是一个**可对账的口径**——预算是宿主给的，
// 宿主需要能自己算一遍并与披露结果对上。计费与交付共用这一份实现，不另写一套。
func MetadataBytes(s Skill) int {
	n := len(s.Name) + len(s.Description)
	for _, r := range s.Resources {
		n += len(r)
	}
	for _, t := range s.Tags {
		n += len(t)
	}
	return n
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
		excluded[normalizeSkillName(e)] = true
	}
	words := tokenize(q.Text)
	mention := strings.TrimSpace(q.Mention)
	// 点名词与资源路径都是**查询级**的不变量：小写化一次，别在技能循环里重复。
	mentionLower := strings.ToLower(mention)
	pathsLower := lowerEach(q.Resources)

	var scored []Scored
	for _, e := range c.skills {
		// lowerName 与 name 同源（New 里都已 TrimSpace），故与 Exclude 归一化后可直比。
		if excluded[e.lowerName] {
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
		switch {
		case len(c.skills) == 0:
			res.Reason = "目录为空（没有已登记的技能）——空结果不代表「模型不知道有这个能力」"
		case allExcluded(c, excluded):
			res.Reason = "目录内技能都已被 Exclude 排除（此前已披露过）——不是「没有这个能力」"
		case len(words) == 0 && mentionLower == "" && len(pathsLower) == 0:
			// 「无相关度信号」与「确无此技能」必须分开：前者是查询没给可检索的输入
			// （或只给了标点），宿主不能据此判断目录里缺能力。
			res.Reason = "查询无相关度信号（点名/描述词/资源路径均为空）——空结果只说明没有可打分的输入，不代表目录里没有该技能"
		default:
			res.Reason = "没有技能命中本次查询（点名/描述/资源三项都没匹配）——" +
				"宿主应据此判断要不要回退到全量披露，而不是当成「没有这个能力」"
		}
		return res, nil
	}

	picked := scored
	if b.Max > 0 && len(picked) > b.Max {
		picked = picked[:b.Max]
		res.Truncated = true
		res.Reason = "候选数超过 Max"
	}
	if b.MaxBytes > 0 {
		kept := make([]Scored, 0, len(picked))
		used := 0
		omitted := 0
		minCost := -1
		for _, sc := range picked {
			n := MetadataBytes(sc.Skill)
			if minCost < 0 || n < minCost {
				minCost = n
			}
			if used+n > b.MaxBytes {
				// 跳过这条，继续看后面的候选：first-fit + break 会让「排序靠前的一条太大」
				// 连带丢掉后面明明放得下的小候选，预算越紧浪费越大（issue #292）。
				omitted++
				continue
			}
			used += n
			kept = append(kept, sc)
		}
		picked = kept
		if omitted > 0 {
			res.Truncated = true
			var reason string
			if len(kept) == 0 {
				// 与「候选过大被跳过」区分：这次是预算相对最小候选仍太小。
				reason = fmt.Sprintf("预算太小：MaxBytes=%d 小于最小候选的元数据 %d 字节，一条都放不下", b.MaxBytes, minCost)
			} else {
				reason = fmt.Sprintf("候选过大：跳过 %d 条超出 MaxBytes 的元数据（已用 %d/%d 字节）", omitted, used, b.MaxBytes)
			}
			if res.Reason == "" {
				res.Reason = reason
			} else {
				res.Reason += "；" + reason
			}
		}
	}
	for _, sc := range picked {
		res.Skills = append(res.Skills, sc.Skill)
	}
	return res, nil
}

// allExcluded 目录内技能是否全部被 Exclude 排除（用于区分空结果的两种原因）。
func allExcluded(c *Catalog, excluded map[string]bool) bool {
	if len(c.skills) == 0 {
		return false
	}
	for _, e := range c.skills {
		if !excluded[e.lowerName] {
			return false
		}
	}
	return true
}
