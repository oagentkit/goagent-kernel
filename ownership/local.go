// local.go —— 单区域退化档：本区域即权威（issue #273）。
//
// 单区域部署没有第二个权威，也不需要跨域转移确认；但提交路径的判据形状
// 必须与共享权威一致——否则「先单区域、后接共享权威」会变成一次调用方
// 改写，而调用方改写的地方越多，「所有权校验」越容易在某个入口被漏掉。
//
// Local 与 MemoryAuthority 共用 Judge：同一个提交声明在两种部署形态下
// 得到同一条判定（单区域只是没有换代路径，不是另一套判据）。
package ownership

import (
	"context"
	"strings"
	"sync"

	"github.com/oekit/pkg/errors"
)

// Local 单区域本地权威：所有提交都由本区域判定，不存在跨区域转移。
//
// 零值不可用，请用 @@NewLocal@@ / @@NewLocalFor@@。
type Local struct {
	region Region

	mu      sync.Mutex
	domains map[Domain]struct{} // nil = 开放域集合（任意任务域按需授予）
	grants  map[Domain]Grant
}

// NewLocal 构造**开放域集合**的单区域权威：任意任务域都归本区域，
// 首次查询时铸造该域的授权（epoch 1）。
//
// 适合任务域在启动时无法枚举的宿主。需要可枚举的映射时用
// @@NewLocalFor@@（只对显式登记的任务域有权威）。
func NewLocal(region Region) (*Local, error) {
	return newLocal(region, nil)
}

// NewLocalFor 构造**显式域集合**的单区域权威：只对 domains 里的任务域
// 有权威，其余一律 ErrUnknownDomain。
func NewLocalFor(region Region, domains ...Domain) (*Local, error) {
	if len(domains) == 0 {
		return nil, errors.Wrap(ErrBadMapping, "限定域集合为空：至少登记一个任务域")
	}
	set := make(map[Domain]struct{}, len(domains))
	for _, d := range domains {
		if strings.TrimSpace(string(d)) == "" {
			return nil, errors.Wrap(ErrBadMapping, "任务域为空")
		}
		set[d] = struct{}{}
	}
	return newLocal(region, set)
}

// newLocal 共享构造路径（校验与非空初始化只有这一份）。
//
// 显式域集合在构造期就把授权铸好：映射里的域「可查即可判」，不依赖调用方
// 先查询过一次；开放域集合按首次 Owner 查询铸造。
func newLocal(region Region, domains map[Domain]struct{}) (*Local, error) {
	if strings.TrimSpace(string(region)) == "" {
		return nil, errors.Wrap(ErrBadMapping, "权威区域为空")
	}
	l := &Local{region: region, domains: domains, grants: map[Domain]Grant{}}
	for d := range domains {
		l.grants[d] = Grant{Domain: d, Region: region, Epoch: 1, Token: mintToken()}
	}
	return l, nil
}

// Owner 返回任务域在本区域的授权（首次查询时铸造，之后稳定复用）。
func (l *Local) Owner(_ context.Context, domain Domain) (Grant, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.domains != nil {
		if _, ok := l.domains[domain]; !ok {
			return Grant{}, unknownDomain(domain)
		}
	}
	if g, ok := l.grants[domain]; ok {
		return g, nil
	}
	g := Grant{Domain: domain, Region: l.region, Epoch: 1, Token: mintToken()}
	l.grants[domain] = g
	return g, nil
}

// Mapping 返回映射快照。
//
// 显式域集合（NewLocalFor）返回全部登记域；开放域集合（NewLocal）只包含
// **已经被查询过**、因而已经授予的域——未被查询的域按需授予，事先无法枚举
// （逐域事实仍可用 Owner 查）。
func (l *Local) Mapping(context.Context) (RegionMap, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(RegionMap, len(l.grants))
	for d := range l.grants {
		out[d] = l.region
	}
	return out, nil
}

// BeginTransfer 单区域没有跨区域转移（显式拒绝，不静默不做）。
func (l *Local) BeginTransfer(context.Context, TransferRequest) (Transfer, error) {
	return Transfer{}, errors.Wrap(ErrSingleRegion,
		"单区域本地权威不支持跨区域转移；多区域部署请接共享权威",
		"region", string(l.region))
}

// ConfirmTransfer 单区域没有转移确认（显式拒绝，不静默不做）。
func (l *Local) ConfirmTransfer(context.Context, ConfirmRequest) (Grant, error) {
	return Grant{}, errors.Wrap(ErrSingleRegion,
		"单区域本地权威不支持跨区域转移；多区域部署请接共享权威",
		"region", string(l.region))
}

// Fence 校验一次提交（判定逻辑与共享权威共用 Judge）。
//
// **只读**：没有授权的任务域一律 ErrUnknownDomain，不会因为一笔提交而
// 顺手铸造授权（判据面不能有分配副作用，否则伪造域名的提交就能撑大状态）。
// 开放域集合下先调用一次 Owner 取得授权，再拿它提交。
func (l *Local) Fence(_ context.Context, c Commit) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.domains != nil {
		if _, ok := l.domains[c.Domain]; !ok {
			return unknownDomain(c.Domain)
		}
	}
	g, ok := l.grants[c.Domain]
	if !ok {
		return unknownDomain(c.Domain)
	}
	return Judge(g, c)
}

// 编译期断言：单区域退化档仍是完整权威面（调用方代码零改写）。
var _ Authority = (*Local)(nil)
