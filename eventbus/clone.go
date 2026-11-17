// clone.go —— 事件的拷贝面（issue #378）。
//
// # 为什么事件要能拷
//
// 载荷 map 原样透传会让**发布方、返回帧、所有订阅方共享同一张 map**：
//
//   - 发布方复用同一张 map 写下一条事件 ⇒ 订阅方看到的是**下一条**的值
//     （顺序污染；事后无法从症状倒推哪条事件被改过）；
//   - 发布方复用 map 与订阅方读载荷并发 ⇒ **数据竞争**，且竞争的是留痕载荷。
//
// 拷贝是**写入侧**的责任：读侧看到的必须是「发布的那一份」，不靠调用方自觉。
// 本包在广播前拷一次（Bus.Publish / DurablePublisher.Publish），落库侧另拷
// 一份（落库行与广播帧是两份独立数据）。
//
// # 边界：同一批订阅方之间仍共享广播帧的那张 map
//
// 逐订阅方拷贝要改投递拓扑（每个订阅方一条 goroutine + 独立 channel），
// 不在本轮范围。订阅方不得改写 ev.Payload——它是只读留痕。
package eventbus

import "maps"

// Clone 拷贝事件：载荷 map 与环节号指针都另起一份。
//
// 载荷是 map（引用语义）、环节号是指针——两者都必须另起一份，否则「拷贝」
// 只是换了个变量名。nil 载荷保持 nil（不凭空造一张空表：nil 与空表在
// JSON 落库面是不同的形态）。
func (e Event) Clone() Event {
	out := e
	out.Payload = maps.Clone(e.Payload)
	out.StepID = cloneStepID(e.StepID)
	return out
}

// cloneStepID 拷贝环节号指针（nil 保持 nil）。
func cloneStepID(id *uint64) *uint64 {
	if id == nil {
		return nil
	}
	v := *id
	return &v
}
