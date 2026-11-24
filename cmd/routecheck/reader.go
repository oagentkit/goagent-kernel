package main

import (
	"bytes"
	"io"
)

// newReader 给 yaml 解码器一个 io.Reader。
func newReader(b []byte) io.Reader {
	return bytes.NewReader(b)
}
