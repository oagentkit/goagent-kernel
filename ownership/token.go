// token.go —— 权威铸造的凭据（授权令牌 / 转移提案标识与回执令牌）。
//
// 与 handoff / taskboard 同一条纪律：凭据由**判据面**铸造，调用方不自行
// 生成。随机源失败时宁可 panic 也不发一份可预测的凭据——可预测的令牌让
// 「凭令牌认人」名存实亡。
package ownership

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// mintToken 铸造授权令牌（128 位随机）。
func mintToken() string {
	return "own-" + mintRandom()
}

// mintReceiptToken 铸造转移回执令牌（与授权令牌区分前缀，便于审计辨认）。
func mintReceiptToken() string {
	return "receipt-" + mintRandom()
}

// mintTransferID 铸造转移提案标识（128 位随机）。
func mintTransferID() TransferID {
	return TransferID("xfer-" + mintRandom())
}

// mintRandom 取 128 位随机数的十六进制形式。
func mintRandom() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 凭据铸造失败不能被降级成「用了个弱凭据」。
		panic(fmt.Sprintf("ownership: 铸造凭据失败: %v", err))
	}
	return hex.EncodeToString(b[:])
}
