// identity.go —— 动作身份 / 持有者身份的铸造原语（从 sideeffect.go 拆出，
// 只为守住「单文件 400 行」的结构阈值；判据归属不变）。
package sideeffect

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/oekit/pkg/encoding"
)

// HashPayload 动作 Payload 的身份摘要（sha256 hex，v0.33.0 后新增）。
//
// 存储实现用它把「同 Key 必须同 Payload」落成可持久化、可比对的定长值
// （Action 身份三元组之一，见 ErrActionConflict）。统一在本包给出：
// 两个参考实现与宿主各自实现哈希算法的话，比对口径会漂——而身份比对
// 恰恰是「同一份事实」的判据，判据本身不能有两份。
//
// nil 与空切片同值（都哈希为空输入）——Payload 缺省即空，不制造
// 「没传」与「传了空」的身份分裂。
func HashPayload(p []byte) string {
	// 单源：oekit/pkg/encoding.Sha256Hex 就是 sha256.Sum256 + hex.EncodeToString
	// （小写十六进制、全长）。本仓不另写一份——哈希算法与编码口径一旦分叉，
	// 「同 Key 同 Payload」的比对会得出两个不同答案（coord 的持久化身份、
	// pgstore 的身份列、sidetest 的替身都读这一个入口）。
	return encoding.Sha256Hex(p)
}

// MintHolder 铸造一份持有者身份（Lease.Holder / Reconcile.Holder 的参考
// 铸造原语，v0.33.0）。
//
// 256 位随机数，不可预测、授予之间不重合。存储实现**可以**用它，也可以用
// 任何不可预测的方案（存储侧序列派生、UUIDv4 等），但铸造必须发生在
// **存储侧的授予动作里**——调用方自选的身份等于没有身份（与 taskboard
// 「令牌由存储铸造」同一条纪律：凭据由判据面铸造）。随机源失败时直接
// panic：宁可拒绝授予，也不发一份可预测的凭据。
func MintHolder() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("铸造持有者身份失败: %v", err))
	}
	return hex.EncodeToString(b[:])
}
