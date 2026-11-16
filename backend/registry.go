// registry.go —— 后端登记簿：登记、对照与选择。
//
// 选择的两条纪律与 sandbox.Registry.Resolve / delegate.Route 同源：
// 无满足者即拒绝（不降级），满足者取最弱（同档按 ID 字典序）。
//
// # 宿主声明方法的调用面（issue #329）
//
// Backend.ID / Isolation 是**宿主实现**的方法：生产后端可能去问配置中心/
// 容器运行时。登记簿只在锁**外**调用它们——锁内只比对登记时缓存下来的
// 纯数据（ID 快照）。否则一个慢 ID() 会拖住全部并发 Register/Candidates，
// 而 ID() 回查登记簿本身还会同 goroutine 二次抢锁 ⇒ 死锁。
package backend

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/oekit/pkg/errors"
)

// ErrDuplicateID 同一 ID 被登记两次。
var ErrDuplicateID = errors.New("backend: 后端 ID 重复登记")

// ErrBadBackend 试图登记不合法的后端（nil / 空 ID）。
var ErrBadBackend = errors.New("backend: 后端不合法")

// entry 一条登记：后端 + 登记时缓存的身份快照。
//
// id 只在 Register 的锁外取一次；锁内的重复判定只读快照，不再问宿主。
type entry struct {
	backend Backend
	id      string
}

// Registry 任务式执行后端的登记簿。
//
// 并发安全：登记与解析都可能来自不同 goroutine，内部加锁。宿主声明方法
// （ID/Isolation）一律在锁外调用（见文件头）。
type Registry struct {
	mu   sync.Mutex
	list []entry // 按登记顺序存放；Resolve 时排序，不依赖登记顺序
}

// Register 登记一个后端。
//
// 拒绝的三种情形都报错而不是「尽力而为」：nil / 空 ID 是声明缺陷，
// ID 重复会让「选到了谁」不可判定。
func (r *Registry) Register(b Backend) error {
	if b == nil {
		return errors.Wrap(ErrBadBackend, "backend: 登记的后端是 nil")
	}
	// 宿主方法在锁外取一次：慢 ID() 只拖慢它自己这次登记，不拖住登记簿。
	id := b.ID()
	if strings.TrimSpace(id) == "" {
		return errors.Wrap(ErrBadBackend, "backend: 登记的后端没有 ID（留痕与选择无从定位）")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, have := range r.list {
		if have.id == id {
			return errors.Wrap(ErrDuplicateID, fmt.Sprintf("backend: ID %q 已被登记", id))
		}
	}
	r.list = append(r.list, entry{backend: b, id: id})
	return nil
}

// snapshot 取登记簿的一致快照（锁内只拷贝，不调宿主方法）。
func (r *Registry) snapshot() []entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.list)
}

// Candidates 返回满足要求的全部后端，按「最弱优先」排序：
// 隔离档位升序，同档按 ID 字典序。
//
// 顺序即选择的依据，且**不依赖登记顺序**——登记顺序通常来自配置文件的
// 书写次序，一次无关的重排就会换掉执行方（delegate.Route 的同源教训）。
//
// 对照判据（SatisfiedBy）与排序取值都在锁外对快照执行：慢后端/回查登记簿
// 的 ID() 不会占住登记簿（issue #329）。排序键（隔离档位与 ID）只各取一次。
func (r *Registry) Candidates(spec Spec) ([]Backend, error) {
	if err := spec.Validate(); err != nil {
		return nil, errors.Wrap(ErrBadSpec, "backend: "+err.Error())
	}
	entries := r.snapshot()

	type candidate struct {
		backend Backend
		id      string
		iso     Isolation
	}
	out := make([]candidate, 0, len(entries))
	for _, e := range entries {
		if spec.SatisfiedBy(e.backend) == nil {
			out = append(out, candidate{backend: e.backend, id: e.id, iso: e.backend.Isolation()})
		}
	}
	// 排序键取登记时缓存的 ID 与本次快照的隔离档位，不再问宿主。
	slices.SortStableFunc(out, func(a, b candidate) int {
		if c := cmp.Compare(a.iso, b.iso); c != 0 {
			return c
		}
		return cmp.Compare(a.id, b.id)
	})
	backends := make([]Backend, len(out))
	for i, c := range out {
		backends[i] = c.backend
	}
	return backends, nil
}

// Resolve 从登记簿里选一个满足要求的后端。
//
// 选择口径：**满足者取最弱**（够用且最省），同档按 ID 字典序。
// 一个都不满足时报 ErrNoBackend，不降级、不挑「差不多的」。
func (r *Registry) Resolve(spec Spec) (Backend, error) {
	cands, err := r.Candidates(spec)
	if err != nil {
		return nil, err
	}
	if len(cands) == 0 {
		return nil, errors.Wrap(ErrNoBackend, fmt.Sprintf(
			"backend: 要求隔离 ≥ %s、出站 %s、预算（mem=%d calls=%d bytes=%d）——登记簿共 %d 个后端，无一满足",
			spec.MinIsolation, spec.Egress,
			spec.Budget.MaxMemory, spec.Budget.MaxCalls, spec.Budget.MaxBytes, len(r.snapshot())))
	}
	return cands[0], nil
}
