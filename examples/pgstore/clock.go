// clock.go —— 存储时钟面（注入 + 读取）：到期时刻由**存储自己的时钟**
// 算，调用方的时钟与存储的时钟可能对不上，跨进程时更不能假设一致。
//
// 本文件是「时钟接缝」的落点：注入（UseClock）与读取（nowIn）放在一起，
// 因为接缝契约说的是**同一个回调**的两端——注入方（宿主）定约，读取方
// （内核）定兑现。
package pgstore

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/oekit/pkg/errors"
)

// UseClock 注入时钟（boardtest 契约套件用）。
//
// # 接缝契约（宿主注入的时钟回调；内核在**持有任务行锁**的事务内同步调用它）
//
// @seam: no-reentry no-blocking panic-isolated
//
//   - no-reentry：宿主注入的时钟**不得回查本包**（不得再调本存储的任何方法，
//     尤其不得在时钟里调 Claim / Renew）：内核调用它时**持有该任务行的锁**
//     （SELECT … FOR UPDATE），回查同一行即两个事务互相等对方持有的行锁：
//     自死锁。时钟必须是纯函数（只读进程内状态）。
//   - no-blocking：宿主注入的时钟必须**立即返回**——内核在持行锁的事务里同步
//     等它返回；它不得做 I/O、不得等锁、不得等别的回调。阻塞会把行锁与整个
//     事务一起拖住，后来者全部堆在这行锁上。
//   - panic-isolated：**宿主侧兑现**——本包**没有 recover 边界**：时钟 panic
//     会沿 Claim / Renew 外溢（事务由 runTx 的 defer 回滚，不留半截状态，
//     但 panic 本身外溢）。宿主注入的时钟须自行保证不 panic。
//
// 内核侧的兑现面：调用点在 nowIn 里——**先取回调、解锁后再调**（issue #317）：
// 取出回调后即释放本包的进程内互斥锁（b.mu），故回调重入本包的互斥锁不会
// 自锁；剩下不可回查的是**数据库行锁**（见上）。
func (b *BoardStore) UseClock(now func() time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if now != nil {
		b.now = now
	}
}

// nowIn 事务内的当前时刻：注入（boardtest 套件）→ 注入值；否则读数据库
// clock_timestamp。必须在**取得行锁之后**调用（与 EffectStore.nowIn 同款
// 理由：等锁时长不得计入租约寿命）。
func (b *BoardStore) nowIn(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	b.mu.Lock()
	injected := b.now
	b.mu.Unlock()
	if injected != nil {
		return injected(), nil
	}
	var t time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&t); err != nil {
		return time.Time{}, errors.Wrap(err, "pgstore: 读数据库时间失败")
	}
	return t, nil
}
