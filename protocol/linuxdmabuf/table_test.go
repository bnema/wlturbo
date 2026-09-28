//go:build linux

package linuxdmabuf_test

import (
	"encoding/binary"
	"github.com/bnema/wlturbo/protocol/linuxdmabuf"
	"testing"
)

func TestTrancheIndices(t *testing.T) {
	b := make([]byte, 4)
	binary.NativeEndian.PutUint16(b, 1)
	binary.NativeEndian.PutUint16(b[2:], 0)
	got, e := linuxdmabuf.TrancheIndices(b, 2)
	if e != nil || len(got) != 2 || got[0] != 1 || got[1] != 0 {
		t.Fatalf("indices %v %v", got, e)
	}
	if _, e = linuxdmabuf.TrancheIndices(b, 1); e == nil {
		t.Fatal("out of range accepted")
	}
	if _, e = linuxdmabuf.TrancheIndices(b[:3], 2); e == nil {
		t.Fatal("odd length accepted")
	}
	if _, e = linuxdmabuf.ReadFormatTable(nil, 0); e == nil {
		t.Fatal("missing fd accepted")
	}
}
