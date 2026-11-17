// mapping.go —— 区域权威映射：**可配置、可校验、可查**的事实（issue #273 验收）。
//
// 映射不是注释里的约定，也不是散落在各宿主的配置项：它在本包是一个
// 可校验的值（Validate）、可查的纯函数（Owner / Domains），并作为
// Authority.Mapping 的一致快照对外给出。任务域与区域都是开放键——内核
// 不解释它们的业务语义或部署拓扑。
package ownership

import (
	"sort"
	"strings"

	"github.com/oekit/pkg/errors"
)

// RegionMap 任务域到权威区域的映射（配置面的事实）。
//
// 不变量：每个登记的任务域恰好一个权威区域；未登记的域没有权威
// （提交/发起转移一律 ErrUnknownDomain，而不是「默认归某个区域」）。
type RegionMap map[Domain]Region

// Validate 校验映射自洽：不得为空，键与值都不得为空白。
//
// 空映射不是「还没配置」的合法中间态：它让每一笔提交都拿到
// ErrUnknownDomain，配置错误与业务错误从此分不开。故在构造/加载期显式拒绝。
func (m RegionMap) Validate() error {
	if len(m) == 0 {
		return errors.Wrap(ErrBadMapping, "权威映射为空：没有任何任务域有权威")
	}
	for d, r := range m {
		if strings.TrimSpace(string(d)) == "" {
			return errors.Wrap(ErrBadMapping, "任务域为空", "region", string(r))
		}
		if strings.TrimSpace(string(r)) == "" {
			return errors.Wrap(ErrBadMapping, "权威区域为空", "domain", string(d))
		}
	}
	return nil
}

// Owner 查询任务域的权威区域（纯查询；未登记返回 false）。
func (m RegionMap) Owner(domain Domain) (Region, bool) {
	r, ok := m[domain]
	return r, ok
}

// Domains 返回全部已登记任务域（字典序确定；快照重复调用结果稳定）。
//
// 顺序必须确定：映射遍历顺序随 map 实现变化，用遍历顺序做配置比对或
// 报错输出会让「同一份配置」产生不同的观测。
func (m RegionMap) Domains() []Domain {
	out := make([]Domain, 0, len(m))
	for d := range m {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
