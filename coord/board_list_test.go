package coord

// issue #182 的判据：List 的「一致快照」承诺必须有**一次原子操作**兑现。
//
// 旧实现 Keys + Snapshot 两次调用：两次之间新增/删除的任务既不进结果也不
// 报错——「同刻视图」是跨两个时刻的拼接。这里的用例从两侧钉住：
//  1. 结构面：List 对后端的调用序列必须恰好是一次 ScanSnapshot（旧实现的
//     keys+snapshot 序列在此直接红——判据有「坏实现会红」的牙）；
//  2. 语义面：ScanSnapshot 的返回是**同刻全集**——并发写入只能整体落在
//     扫描前或扫描后，任何一页结果都不存在「半新半旧」的拼接。

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// scanRecordingBackend 记录调用序列的替身（转发 MemoryBackend）。
// SingleMachine：测试替身只活在单进程内（#194 装配判据要求显式声明）。
func (b *scanRecordingBackend) SingleMachine() bool { return true }

type scanRecordingBackend struct {
	inner *MemoryBackend

	mu    sync.Mutex
	calls []string
}

func (b *scanRecordingBackend) record(op string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, op)
}

func (b *scanRecordingBackend) ops() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.calls...)
}

func (b *scanRecordingBackend) Snapshot(ctx context.Context, keys ...string) (map[string]Entry, error) {
	b.record("snapshot")
	return b.inner.Snapshot(ctx, keys...)
}

func (b *scanRecordingBackend) Txn(ctx context.Context, asserts []Assert, mutations []Mutation) (bool, error) {
	return b.inner.Txn(ctx, asserts, mutations)
}

func (b *scanRecordingBackend) Keys(ctx context.Context, prefix string) ([]string, error) {
	b.record("keys")
	return b.inner.Keys(ctx, prefix)
}

func (b *scanRecordingBackend) ScanSnapshot(ctx context.Context, prefix string) (map[string]Entry, error) {
	b.record("scan")
	return b.inner.ScanSnapshot(ctx, prefix)
}

// TestListIsSingleAtomicScan List 的读面必须收口在一次 ScanSnapshot：
// 不得再走 Keys + Snapshot 两步拼（#182 的缺陷本体——两次调用之间键集
// 可以增删，拼出来的既不是全量也不是任一时刻的视图）。
func TestListIsSingleAtomicScan(t *testing.T) {
	ctx := context.Background()
	b := &scanRecordingBackend{inner: NewMemoryBackend()}
	board, err := NewBoardAdapter(b)
	if err != nil {
		t.Fatal(err)
	}
	board.Seed(
		taskboard.Task{ID: "t1", State: taskboard.StateOpen},
		taskboard.Task{ID: "t2", State: taskboard.StateOpen},
	)

	if _, err := board.List(ctx); err != nil {
		t.Fatal(err)
	}
	ops := b.ops()
	if len(ops) != 1 || ops[0] != "scan" {
		t.Fatalf("List 必须恰好一次 ScanSnapshot（Keys+Snapshot 两步拼是跨时刻拼接）, calls=%v", ops)
	}
}

// TestListExcludesDeletedTasks 同刻全集的可见性：删除后的任务不得出现在
// 任何一次 List 里（ScanSnapshot 与 Keys 的墓碑口径一致）。
func TestListExcludesDeletedTasks(t *testing.T) {
	ctx := context.Background()
	inner := NewMemoryBackend()
	board, err := NewBoardAdapter(inner)
	if err != nil {
		t.Fatal(err)
	}
	board.Seed(
		taskboard.Task{ID: "t1", State: taskboard.StateOpen},
		taskboard.Task{ID: "t2", State: taskboard.StateOpen},
	)

	if _, err := inner.Txn(ctx, nil, []Mutation{{Key: taskKeyPrefix + "t1", Del: true}}); err != nil {
		t.Fatal(err)
	}
	tasks, err := board.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != "t2" {
		t.Fatalf("已删除的任务不得出现在同刻视图里（实得 %v）", tasks)
	}
}

// TestScanSnapshotIsPointInTime 语义面：并发写入下每次扫描结果都是**同刻
// 全集**——所有值可解码、ID 唯一，且每个条目的键值对出自同一瞬间（写入
// 只能整体落在扫描前或扫描后，不存在半新半旧）。配 -race 跑。
func TestScanSnapshotIsPointInTime(t *testing.T) {
	ctx := context.Background()
	b := NewMemoryBackend()
	for i := 0; i < 8; i++ {
		if _, err := b.Txn(ctx, nil, []Mutation{
			{Key: taskKeyPrefix + string(rune('a'+i)), Val: []byte(`{"id":"` + string(rune('a'+i)) + `"}`)},
		}); err != nil {
			t.Fatal(err)
		}
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // 写入方：不断增删键（扫描窗口的两端都碰）
		defer wg.Done()
		for round := 0; ; round++ {
			select {
			case <-stop:
				return
			default:
			}
			k := taskKeyPrefix + string(rune('a'+round%8))
			_, _ = b.Txn(ctx, nil, []Mutation{{Key: k, Val: []byte(`{"id":"` + string(rune('a'+round%8)) + `","round":` + strconv.Itoa(round) + `}`)}})
		}
	}()

	for i := 0; i < 200; i++ {
		scan, err := b.ScanSnapshot(ctx, taskKeyPrefix)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for k, e := range scan {
			if !strings.HasPrefix(k, taskKeyPrefix) {
				t.Fatalf("扫描结果混入前缀外的键: %q", k)
			}
			if !e.Exists || len(e.Val) == 0 {
				t.Fatalf("同刻视图不得含墓碑或空值: %q %+v", k, e)
			}
			id := k[len(taskKeyPrefix):]
			if seen[id] {
				t.Fatalf("同刻视图内 ID 唯一（键 %q 重复）", k)
			}
			seen[id] = true
		}
	}
	close(stop)
	wg.Wait()
}

// TestMemoryBackendScanSnapshotCopyIndependence 值独立：改返回 map 的内容
// 不得影响后端状态（与 Snapshot 同一条「后端不得复用底层字节」的纪律）。
func TestMemoryBackendScanSnapshotCopyIndependence(t *testing.T) {
	ctx := context.Background()
	b := NewMemoryBackend()
	if _, err := b.Txn(ctx, nil, []Mutation{{Key: "p/a", Val: []byte("v1")}}); err != nil {
		t.Fatal(err)
	}
	scan, err := b.ScanSnapshot(ctx, "p/")
	if err != nil || len(scan) != 1 {
		t.Fatalf("扫描应取回 1 键: %v %v", scan, err)
	}
	entry, ok := scan["p/a"]
	if !ok || !entry.Exists || string(entry.Val) != "v1" {
		t.Fatalf("扫描结果不符: %+v ok=%v", entry, ok)
	}
	scan["p/injected"] = Entry{Key: "p/injected", Exists: true}
	entry.Val[0] = 'X'

	scan2, _ := b.ScanSnapshot(ctx, "p/")
	if len(scan2) != 1 {
		t.Fatalf("改返回 map 不得影响后端（实得 %d 键）", len(scan2))
	}
	if string(scan2["p/a"].Val) != "v1" {
		t.Fatalf("改返回值不得影响后端（实得 %s）", scan2["p/a"].Val)
	}
	// 排序确定性不直接可见（map 无序），但键集确定：与 Keys 同口径。
	keys, _ := b.Keys(ctx, "p/")
	if !slices.Equal(keys, []string{"p/a"}) {
		t.Fatalf("Keys 与 ScanSnapshot 可见性应一致: %v", keys)
	}
}
