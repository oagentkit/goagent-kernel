package tooldisclose

// Schema 的表示、设界与拷贝。
//
// 单独成文件是为了让「schema 的引用字段在哪里被复制、在哪里被裁剪」集中可见：
// 构造期与所有导出出口共用 cloneSchema；设界逻辑（参数描述上限 / 总字节上限）
// 只在这一个文件里。

import (
	"maps"
	"slices"

	"github.com/oekit/pkg/stringx"
)

// Schema 工具输入 schema 的最小载体。
//
// 为什么自定一个结构而不是收 map[string]any：占位的「退化」必须**可机械判定**，
// 否则宿主无法保证「未加载的工具不泄露真 schema」。宿主把自己的 schema 投影进来即可，
// 本包不要求与宿主的 JSON Schema 库同型。
type Schema struct {
	// Type JSON Schema 顶层类型；退化占位恒为 DegenerateType。
	Type string
	// Properties 参数定义；退化占位恒为 nil。
	Properties map[string]Param
	// Required 必填参数名；退化占位恒为 nil。
	Required []string
	// Truncated 构造期是否裁剪过 schema（参数描述截断或参数项超 MaxSchemaBytes 被丢弃）。
	// 宿主可据此决定是否回源取完整定义；未裁剪恒为 false。
	Truncated bool
}

// Param 单个入参的最小定义。
type Param struct {
	Type        string
	Description string
}

// Degenerate 空 schema：声明不了任何参数，故任何调用都必然参数非法。
//
// 这一点是**故意**的：占位若碰巧还能被调用，模型就会把占位当契约继续瞎编参数，
// 门禁也就没机会把它挡回来。
func Degenerate() Schema { return Schema{Type: DegenerateType} }

// IsDegenerate 是否为空 schema 占位。
func (s Schema) IsDegenerate() bool { return s.Type == DegenerateType }

const (
	// MaxParamDescriptionLen 单个参数描述的字符上限（按 rune 计）。
	//
	// 参数描述与工具描述一样是宿主自由文本；不设界时「单个工具占多少」没有上界
	// （50 个参数 × 5000 字 ≈ 750 KB 全量留在目录里，issue #290）。截断只削文档，
	// 不改参数名/类型与 Required 语义。
	MaxParamDescriptionLen = 320

	// MaxSchemaBytes 单个工具 schema 的内容字节上限（Type + 参数名/类型/描述 + Required）。
	//
	// 超过时按确定顺序丢弃参数项（先丢最大的、同大小按名字升序）并同步过滤 Required，
	// 置 Schema.Truncated=true。上界按**内容字节**计，不含 map/slice 容器开销。
	MaxSchemaBytes = 64 << 10
)

// schemaBytes schema 的内容字节数（不含容器开销）：用于核对 MaxSchemaBytes 上界。
func schemaBytes(s Schema) int {
	n := len(s.Type)
	for name, p := range s.Properties {
		n += len(name) + len(p.Type) + len(p.Description)
	}
	for _, r := range s.Required {
		n += len(r)
	}
	return n
}

// propertyBytes 单个参数项的内容字节数（含键名）。
func propertyBytes(name string, p Param) int {
	return len(name) + len(p.Type) + len(p.Description)
}

// sanitizeSchema 深拷贝 schema 并落到声明的占用上界内。
//
// 两步，顺序固定：
//  1. 单参描述截到 MaxParamDescriptionLen（只削文档，不改参数名/类型）；
//  2. 内容字节仍超 MaxSchemaBytes 时，按「最大项优先、同大小名字升序」确定性丢弃参数项，
//     并同步过滤 Required；置 Truncated=true。
//
// 为什么丢弃而不是继续截参数名/类型：名字是调用契约的键，截断会产出无法调用的 schema；
// 丢弃是显式缩小契约，且有 Truncated 标记让宿主知道「这里不是完整定义」。
func sanitizeSchema(s Schema) Schema {
	out := cloneSchema(s)
	truncated := out.Truncated
	if n := len(out.Type); n > MaxParamDescriptionLen {
		out.Type, _ = stringx.TruncRunes(out.Type, MaxParamDescriptionLen)
		truncated = true
	}
	for name, p := range out.Properties {
		if d, clipped := stringx.TruncRunes(p.Description, MaxParamDescriptionLen); clipped {
			p.Description = d
			out.Properties[name] = p
			truncated = true
		}
	}
	trimmed := false
	if schemaBytes(out) > MaxSchemaBytes {
		trimmed = true
		names := slices.Sorted(maps.Keys(out.Properties))
		for schemaBytes(out) > MaxSchemaBytes && len(names) > 0 {
			drop := 0
			for i := 1; i < len(names); i++ {
				ci := propertyBytes(names[i], out.Properties[names[i]])
				cd := propertyBytes(names[drop], out.Properties[names[drop]])
				if ci > cd || (ci == cd && names[i] < names[drop]) {
					drop = i
				}
			}
			delete(out.Properties, names[drop])
			names = append(names[:drop], names[drop+1:]...)
		}
	}
	if trimmed {
		kept := out.Required[:0]
		for _, r := range out.Required {
			if _, ok := out.Properties[r]; ok {
				kept = append(kept, r)
			}
		}
		if len(kept) == 0 {
			out.Required = nil
		} else {
			out.Required = slices.Clone(kept)
		}
		truncated = true
	}
	out.Truncated = truncated
	return out
}

// cloneSchema 深拷贝 schema 的引用字段；nil 保持 nil（退化 schema 的可判性依赖它）。
func cloneSchema(s Schema) Schema {
	s.Properties = maps.Clone(s.Properties)
	s.Required = slices.Clone(s.Required)
	return s
}

// cloneTool 深拷贝目录成员的出口形态。所有导出出口都必须经过它。
func cloneTool(t Tool) Tool {
	t.Schema = cloneSchema(t.Schema)
	return t
}
