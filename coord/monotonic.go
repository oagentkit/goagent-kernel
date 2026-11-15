// monotonic.go —— Backend **版本单调契约**的可跑判据（issue #181）。
//
// # 为什么删除也要判
//
// 「版本是断言的锚点」立论的前提是版本随每次成功写入推进且**永不复用**。
// 删除丢版本的实现（条目随删，同名键重建后版本从 1 重来）会制造教科书
// ABA：快照读到 k@1 ⇒ 另一路删 k ⇒ 重建 k ⇒ 旧断言 k@1 重新成立 ⇒
// 提交成功——判定读的是「上一代的值」，提交写的是「新一代的键」，而且
// **看起来都提交成功**，宿主侧没有任何信号。
//
// 本判据把「写 → 删 → 重建 → 旧版本 CAS 必须失败」钉成红线。与
// taskboard/boardtest 同一口径的共享契约：宿主接 Redis/etcd/PG 时对
// 自己的 Backend 实现跑同一套，不为任何实现开后门。
package coord

import (
	"context"
	"fmt"

	"github.com/oekit/pkg/errors"
)

// VersionNotReused 版本单调契约判据：同一键「写 → 删 → 重建」全序列里
// 版本严格前进、永不复用，删除前（及重建前任何历史）版本做的断言对
// 重建后的键必须失败。
//
// 判法：
//
//  1. 写入 v1，取版本 a；
//  2. 删除：快照必须 Exists=false 且版本 > a——删除同样推进版本
//     （墓碑条目或全局修订号皆可，「删除后版本比删除前小或回
//     VerMissing」必红：锚点随删除丢失就是把它拆了）；
//  3. 重建 v2，取版本 c：必须 > a（不复用历史版本；c == a 即 ABA）；
//  4. 断言 k@a 的 CAS 必须**不生效**（applied=false），且键停在 c/v2
//     （失败的断言一个 Mutation 都不执行）；
//  5. 断言 VerMissing（「从未写过」）对重建后的键也必须不生效——
//     「写过又删了」正是断言要拦的窗口。
//
// 返回的 error 只表示**契约违反或基础设施故障**，不表示断言失败
// （第 4/5 步断言失败是**期望的结论**，不生效才算过）。
func VersionNotReused(ctx context.Context, b Backend, key string) error {
	if b == nil {
		return errors.New("coord: 版本单调判据缺后端")
	}
	if key == "" {
		return errors.New("coord: 版本单调判据缺键名")
	}

	// ① 写入 v1。
	applied, err := b.Txn(ctx, nil, []Mutation{{Key: key, Val: []byte("v1")}})
	if err != nil {
		return errors.Wrap(err, "coord: 判据写入 v1 失败")
	}
	if !applied {
		return errors.New("coord: 判据写入 v1 被空断言拒绝——Txn 对无条件写入不得返回 applied=false")
	}
	snap, err := b.Snapshot(ctx, key)
	if err != nil {
		return errors.Wrap(err, "coord: 判据取版本 a 失败")
	}
	a := snap[key].Ver
	if !snap[key].Exists {
		return fmt.Errorf("coord: 写入 v1 后键必须存在（实得 %+v）", snap[key])
	}

	// ② 删除：Exists=false 且版本前进（墓碑）。
	if applied, err := b.Txn(ctx, nil, []Mutation{{Key: key, Del: true}}); err != nil || !applied {
		return fmt.Errorf("coord: 判据删除失败（err=%v applied=%v）", err, applied)
	}
	snap, err = b.Snapshot(ctx, key)
	if err != nil {
		return errors.Wrap(err, "coord: 判据读删除后状态失败")
	}
	if snap[key].Exists {
		return fmt.Errorf("coord: 删除后键必须 Exists=false（实得 %+v）", snap[key])
	}
	if snap[key].Ver <= a {
		return fmt.Errorf("coord: 删除必须推进版本（删前 %d → 删后 %d）——版本锚点随条目一起删，同名键重建后旧断言会误判「没动过」（issue #181）", a, snap[key].Ver)
	}

	// ③ 重建 v2：版本继续前进，不复用历史。
	if applied, err := b.Txn(ctx, nil, []Mutation{{Key: key, Val: []byte("v2")}}); err != nil || !applied {
		return fmt.Errorf("coord: 判据重建失败（err=%v applied=%v）", err, applied)
	}
	snap, err = b.Snapshot(ctx, key)
	if err != nil {
		return errors.Wrap(err, "coord: 判据读重建后状态失败")
	}
	c := snap[key].Ver
	if !snap[key].Exists || string(snap[key].Val) != "v2" {
		return fmt.Errorf("coord: 重建后键必须存在且值为 v2（实得 %+v）", snap[key])
	}
	if c <= a {
		return fmt.Errorf("coord: 重建后版本 %d 不得 ≤ 删除前版本 %d——版本不复用，c == a 即 ABA（issue #181）", c, a)
	}

	// ④ 删除前的旧版本断言必须不生效（这是期望的结论，不是失败）。
	applied, err = b.Txn(ctx, []Assert{{Key: key, Ver: a}}, []Mutation{{Key: key, Val: []byte("注入")}})
	if err != nil {
		return errors.Wrap(err, "coord: 旧版本断言的 Txn 基础设施故障")
	}
	if applied {
		return fmt.Errorf("coord: 旧版本 %d 的断言在重建后的键上提交成功——版本复用了，ABA 防线破（issue #181）", a)
	}
	snap, err = b.Snapshot(ctx, key)
	if err != nil {
		return errors.Wrap(err, "coord: 判据收尾快照失败")
	}
	if snap[key].Ver != c || string(snap[key].Val) != "v2" {
		return fmt.Errorf("coord: 失败断言的事务不得写入（版本/值被改： %+v，应为 %d/v2）", snap[key], c)
	}

	// ⑤ 「从未写过」断言对重建后的键同样必须不生效。
	applied, err = b.Txn(ctx, []Assert{{Key: key, Ver: VerMissing}}, []Mutation{{Key: key, Val: []byte("注入")}})
	if err != nil {
		return errors.Wrap(err, "coord: VerMissing 断言的 Txn 基础设施故障")
	}
	if applied {
		return errors.New("coord: VerMissing 断言在重建后的键上提交成功——「写过又删了」的窗口没被断言拦住（issue #181）")
	}
	return nil
}
