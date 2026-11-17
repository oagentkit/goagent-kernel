// policy_seams.go —— policy 的 seams 段（接缝契约门禁的声明面）。
//
// 单独成文件的原因与 policy_tenant.go / policy_egress.go 同款：policy.go 已经
// 顶到 max_file_lines（400 行），而它**还要继续长出声明段**——阈值一旦逼着人
// 停止加声明，那条阈值就在保护错误的那个量。
//
// 划界按**用途**：这里全是接缝门禁的声明面类型，不认识判定逻辑。

package archcheck

// Seams 是接缝契约门禁的声明面（policy 的 seams 段）。
//
// # 三类接缝
//
//   - func-field：导出结构体的导出函数型字段（宿主填回调、内核调）；
//   - func-param：导出函数或方法（含导出构造函数）的函数型入参；
//   - iface：导出接口类型（宿主实现、内核调；一条接缝覆盖其全部方法）。
//
// # 契约标记
//
// 接缝声明处的文档注释里必须有一行：
//
//	// @seam: no-reentry no-blocking panic-isolated
//
// # 根本局限
//
// 标记是**承诺**不是事实：声明了却仍在锁内调，本门禁照样绿。
// 它保证「契约被写下」，不保证「契约被兑现」。
//
// # 清单由门禁维护
//
// registry 里的 id 必须**由扫描器产出后逐条誊入**，不许手写臆测。
type Seams struct {
	// Enabled 关闭开关（缺省即启用）。
	Enabled *bool `yaml:"enabled"`
	// Severity 违规级别（缺省即 error）。
	Severity string `yaml:"severity"`
	// Desc 报告里显示的规则说明。
	Desc string `yaml:"desc"`
	// Registry 是接缝清单（逐条登记）。
	Registry []SeamRef `yaml:"registry"`
}

// on 报告接缝契约门禁是否启用（段缺省即启用）。
func (s *Seams) on() bool {
	if s == nil {
		return true
	}
	return s.Enabled == nil || *s.Enabled
}

// SeamRef 是接缝清单里的一条登记。
type SeamRef struct {
	// ID 是接缝的稳定标识（见 Seam 的各 Kind 的 id 形状）。
	ID string `yaml:"id"`
	// Notes 这条接缝为什么是接缝（写给人看的那一句，可空）。
	Notes string `yaml:"notes"`
}
