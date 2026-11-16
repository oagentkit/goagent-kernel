package tooldisclose

// schema 侧的占用上界（#290）：包注释承诺「目录占用有上界」，而真正的无界点是
// schema——参数个数与参数自由文本（参数名/类型/描述）与工具描述一样是宿主给的、
// 长度不受本包控制。50 参 × 5000 字的 schema 能让单条目录成员持有数百 KB，
// 「不必逐条自查」就等于放任无界。本文件把上界落成可核对的常量与一次构造期收界。

import (
	"slices"

	"github.com/oekit/pkg/stringx"
)

const (
	// MaxParams 单个工具 schema 的参数个数上限，超出在构造期裁掉（规则见 boundSchema）。
	//
	// 为什么是 64：参考实现的工具入参面在个位数到十几个参数的量级，64 已是十倍
	// 余量；超过它的参数面模型也读不完，只会在上下文里白占位置。裁剪规则必须
	// 确定，否则同一份注册表两次构造得到两个目录。
	MaxParams = 64

	// MaxParamTextLen schema 内单个自由文本字段（参数名 / 参数类型 / 参数描述 /
	// Required 项）的字符上限（按 rune 计），超出在构造期截断。
	//
	// 与 MaxDescriptionLen 同口径：同为宿主自由文本，同一理由截断而不是写进文档
	// 靠自觉。不设界，「目录占用有上界」的承诺就被参数描述偷换——描述截到
	// MaxDescriptionLen 而每个参数描述可以无限长。
	MaxParamTextLen = 320
)

// boundSchema 把 schema 收进有界形态；存储值即交付值（与描述同一条纪律——
// 检索与派生读的都是存储值，构造期不收界，渲染时再收就是两套事实）。
//
// 裁剪规则（全部确定性，与 map 序无关）：
//
//   - 参数个数超出 MaxParams：**必填优先**（按 Required 声明序），其余按参数名
//     升序补足到上限，仍超出的丢弃；丢弃的参数同步从 Required 里剔除。必填优先
//     的理由：必填是工具契约的核心，丢弃必填等于改变调用语义；可选少一个只是
//     少一种传法。
//   - 每个自由文本字段截到 MaxParamTextLen：参数名、类型、描述、Required 项。
//     参数名截断后撞名时**首次出现处胜出**（与目录重名规则同款）。
//
// 不动的东西：宿主本来就声明的悬空 Required（点名了 Properties 里没有的参数）
// 保持原样——那是 SignatureOf 要当场报错的 schema 不自洽（既有契约），
// 构造期不得悄悄洗掉。
func boundSchema(s Schema) Schema {
	if len(s.Properties) == 0 && len(s.Required) == 0 {
		return s // 退化占位：nil Properties / nil Required，无需收界
	}

	names := make([]string, 0, len(s.Properties))
	for p := range s.Properties {
		names = append(names, p)
	}
	slices.Sort(names)

	keep := names
	var droppedByCap map[string]bool
	if len(names) > MaxParams {
		// 名单超限：必填优先，其余按名升序补足。
		keep = make([]string, 0, MaxParams)
		picked := make(map[string]bool, MaxParams)
		for _, p := range s.Required {
			if _, ok := s.Properties[p]; ok && !picked[p] && len(keep) < MaxParams {
				picked[p] = true
				keep = append(keep, p)
			}
		}
		for _, p := range names {
			if len(keep) >= MaxParams {
				break
			}
			if !picked[p] {
				picked[p] = true
				keep = append(keep, p)
			}
		}
		// 记下因上限被裁的参数：只有它们的 Required 点名要被剔除，
		// 宿主本来就悬空的点名不在此列。
		droppedByCap = make(map[string]bool, len(names)-len(keep))
		for _, p := range names {
			if !picked[p] {
				droppedByCap[p] = true
			}
		}
	}

	// 重建 Properties：字段截到上限；截断撞名时首次出现处胜出（keep 有序，逐字可比）。
	props := make(map[string]Param, len(keep))
	for _, p := range keep {
		key := truncateParamText(p)
		if _, dup := props[key]; dup {
			continue
		}
		param := s.Properties[p]
		param.Type = truncateParamText(param.Type)
		param.Description = truncateParamText(param.Description)
		props[key] = param
	}
	s.Properties = props

	if s.Required != nil {
		req := make([]string, 0, len(s.Required))
		for _, p := range s.Required {
			if droppedByCap[p] {
				continue // 只剔除「因上限被裁」的参数点名
			}
			req = append(req, truncateParamText(p))
		}
		s.Required = req
	}
	return s
}

// truncateParamText schema 内自由文本的统一截断；未超长原样返回（不复制）。
func truncateParamText(s string) string {
	out, _ := stringx.TruncRunes(s, MaxParamTextLen)
	return out
}
