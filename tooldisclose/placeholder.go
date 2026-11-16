package tooldisclose

import (
	"unicode/utf8"

	"github.com/oekit/pkg/stringx"
)

// PlaceholderNotice 未加载工具描述里追加的取回指令。
//
// 为什么指令必须落在**描述**里而不是指望空 schema 自己表达：模型读的是描述里的祈使句。
// 一个「空 schema」不会被它理解成「禁止调用」，只会被理解成「这工具不需要参数」——
// 于是它开始照着描述瞎编参数，门禁也就没机会把这次调用挡回来。
// 指令写死在这里而不做成可变文案：它是本包的核心机制提示，允许宿主改写等于
// 允许宿主无声地关掉「先取回再调用」这道闸。
const PlaceholderNotice = "（schema 未加载：先用检索工具按关键词取回本工具的完整 schema，再调用。）"

// PlaceholderDescription 给工具描述追加取回指令（原文保留在前，宿主可自行决定是否换文案，
// 但必须保留「先取回 schema 再调用」这句语义）。
//
// 原文按「MaxDescriptionLen − 指令长度」截断：追加指令后模型实际收到的描述仍不得超过
// 声明的单条上限。旧实现把指令无条件追加在已截到 MaxDescriptionLen 的描述后面，
// 实际交付 363 rune > 320（issue #290）。
func PlaceholderDescription(description string) string {
	room := MaxDescriptionLen - utf8.RuneCountInString(PlaceholderNotice)
	if room < 0 {
		room = 0
	}
	d, _ := stringx.TruncRunes(description, room)
	return d + PlaceholderNotice
}

// Placeholder 构造未加载工具的占位披露面：退化 schema + 带取回指令的描述。
//
// 单独立为函数（而不是只藏在目录内部）：宿主拼自定义工具面、或测试替身要复刻这套语义时，
// 都能取到同一份实现，不至于各写一版慢慢分叉。
func Placeholder(name, description string) Exposure {
	return Exposure{
		Name:        name,
		Description: PlaceholderDescription(description),
		Schema:      Degenerate(),
		Loaded:      false,
	}
}
