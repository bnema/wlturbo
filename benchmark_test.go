package wlturbo

import "testing"

// BenchmarkWireDispatch isolates framed typed dispatch from socket scheduling.
func BenchmarkWireDispatch(b *testing.B) {
	d := newDisplay(&chunkConn{})
	p := &benchProxy{BaseProxy: BaseProxy{id: 7, context: d.context}}
	d.context.Register(p)
	f := receivedFrame{object: 7, opcode: 0, body: []byte{1, 0, 0, 0}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := d.dispatchFrame(f); err != nil {
			b.Fatal(err)
		}
	}
}

type benchProxy struct {
	BaseProxy
	value uint32
}

func (p *benchProxy) EventSignature(op uint16) (string, bool) { return "uint,", op == 0 }
func (p *benchProxy) Dispatch(e *Event)                       { p.value = e.Uint32() }
