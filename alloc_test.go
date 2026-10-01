package wlturbo

import "testing"

// The README promises allocation-free numeric paths. Pin both halves: a typed
// numeric request and the dispatch of a numeric event to a proxy.
func TestNumericPathsDoNotAllocate(t *testing.T) {
	t.Run("RequestArgs", func(t *testing.T) {
		d := newDisplay(&chunkConn{})
		p := &BaseProxy{id: d.AllocateID(), context: d.context}
		d.context.Register(p)
		req := Request{Proxy: p, Opcode: 1, Name: "test.numeric"}
		send := func() {
			if err := d.context.RequestArgs(req, ArgUint(1), ArgInt(-2), ArgFixed(3)); err != nil {
				t.Fatal(err)
			}
		}
		send() // warm up buffers
		if n := testing.AllocsPerRun(100, send); n != 0 {
			t.Fatalf("RequestArgs allocated %v times per call, want 0", n)
		}
	})

	t.Run("DispatchEvent", func(t *testing.T) {
		d := newDisplay(&chunkConn{})
		p := &benchProxy{BaseProxy: BaseProxy{id: 7, context: d.context}}
		d.context.Register(p)
		f := receivedFrame{object: 7, opcode: 0, body: []byte{42, 0, 0, 0}}
		dispatch := func() {
			if err := d.dispatchFrame(f); err != nil {
				t.Fatal(err)
			}
		}
		dispatch() // warm up eventPool
		if n := testing.AllocsPerRun(100, dispatch); n != 0 {
			t.Fatalf("numeric event dispatch allocated %v times per call, want 0", n)
		}
		if p.value != 42 {
			t.Fatalf("proxy received %d, want 42", p.value)
		}
	})
}
