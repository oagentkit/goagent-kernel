package sideeffect

import (
	"testing"
)

// hash_test.go —— HashPayload 的**格式钉住**（不是「跟上游同实现」的自证）。
//
// 为什么要有这条：HashPayload 是动作身份三元组之一（ErrActionConflict 的判据），
// 它的输出会**持久化进数据库并跨版本比对**——算法或编码口径一旦变化，同一个
// Payload 在旧行与新行上算出不同摘要，所有既有 Key 都会变成「身份不一致」，
// 表现为处处 ErrActionConflict（比崩溃更难查：库里的行看起来完好）。
//
// 故这里钉的是**对外承诺的字节形态**（小写十六进制、全长 64 字符、空与 nil 同值），
// 而不是「调用了哪个函数」。实现可以换成任何等价路径，形态不许动。
func TestHashPayloadFormatPinned(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"空输入", []byte{}, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"nil 与空同值", nil, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"单字节", []byte("p"), "148de9c5a7a44d19e56cd9ae1a554bf67847afb0c58f6e12fa29ac7ddfca9940"},
		{"多字节", []byte("payload"), "239f59ed55e737c77147cf55ad0c1b030b6d7ee748a7426952f9b852d5a935e5"},
	}
	for _, c := range cases {
		got := HashPayload(c.in)
		if got != c.want {
			t.Fatalf("%s：HashPayload 的形态变了（旧行与新行会算出不同摘要，全部 Key 判身份冲突）；want %q, got %q", c.name, c.want, got)
		}
		if len(got) != 64 {
			t.Fatalf("%s：摘要必须是全长 64 字符小写十六进制，实得 %d 字符", c.name, len(got))
		}
	}
}
