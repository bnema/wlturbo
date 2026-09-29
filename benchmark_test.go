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

// BenchmarkDispatchCoalesced measures framing when one read carries many
// messages; per-frame cost must not grow with the amount buffered.
func BenchmarkDispatchCoalesced(b *testing.B) {
	var chunk []byte
	for len(chunk)+12 <= readChunkSize {
		chunk = append(chunk, message(7, 0, []byte{1, 0, 0, 0})...)
	}
	frames := len(chunk) / 12
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		d := newDisplay(&chunkConn{chunks: [][]byte{append([]byte(nil), chunk...)}})
		d.context.Register(&benchProxy{BaseProxy: BaseProxy{id: 7, context: d.context}})
		b.StartTimer()
		for j := 0; j < frames; j++ {
			if err := d.Dispatch(); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*frames), "ns/frame")
}

type benchProxy struct {
	BaseProxy
	value uint32
}

func (p *benchProxy) EventSignature(op uint16) (string, bool) { return "uint,", op == 0 }
func (p *benchProxy) Dispatch(e *Event)                       { p.value = e.Uint32() }

// BenchmarkSendRequest measures marshaling a typical fixed-size request.
func BenchmarkSendRequest(b *testing.B) {
	d := newDisplay(&chunkConn{})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := d.SendRequest(9, 0, uint32(1), int32(2), Fixed(3)); err != nil {
			b.Fatal(err)
		}
	}
}
