//go:build linux

package protocol_test

import (
	"encoding/binary"
	"net"
	"os"
	"testing"
	"time"

	"github.com/bnema/wlturbo"
	"github.com/bnema/wlturbo/protocol/core"
	"github.com/bnema/wlturbo/protocol/cursorshape"
	"github.com/bnema/wlturbo/protocol/fractionalscale"
	"github.com/bnema/wlturbo/protocol/viewporter"
	"golang.org/x/sys/unix"
)

// Allocation profile of generated bindings, measured against the current
// transport (see the regression tests below for what is pinned):
//
//   - Event dispatch of numeric arguments (uint/int/fixed) is allocation-free.
//   - Zero-argument requests are allocation-free.
//   - Generated requests pass typed wl.Arg values to Context.RequestArgs, so
//     numeric requests are allocation-free for every value. (The legacy
//     Context.Request boxed each argument into an interface: one heap
//     allocation per 32-bit value >= 256, e.g. set_source 4, set_destination
//     2, set_shape 1 before this path existed.)
//   - Allocations that are inherent to the operation are kept separate below:
//     new_id child creation (proxy object + map entries), received strings
//     (Event.String copies), and received file descriptors (OwnedFD).

// loopConn is an in-memory net.Conn. Reads replay a fixed byte pattern made of
// whole frames (never splitting one), and writes are discarded, so a benchmark
// measures generated-binding and transport cost without socket scheduling.
type loopConn struct{ pattern []byte }

func (c *loopConn) Read(p []byte) (int, error) {
	n := len(p) / len(c.pattern) * len(c.pattern)
	if n == 0 {
		return 0, net.ErrClosed
	}
	for off := 0; off < n; off += len(c.pattern) {
		copy(p[off:], c.pattern)
	}
	return n, nil
}
func (c *loopConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *loopConn) Close() error                     { return nil }
func (c *loopConn) LocalAddr() net.Addr              { return loopAddr{} }
func (c *loopConn) RemoteAddr() net.Addr             { return loopAddr{} }
func (c *loopConn) SetDeadline(time.Time) error      { return nil }
func (c *loopConn) SetReadDeadline(time.Time) error  { return nil }
func (c *loopConn) SetWriteDeadline(time.Time) error { return nil }

type loopAddr struct{}

func (loopAddr) Network() string { return "loop" }
func (loopAddr) String() string  { return "loop" }

// loopEnv is a display over loopConn with one registered object of each kind
// used by the tests. Tests replay an event by assigning its frame to c.pattern.
type loopEnv struct {
	d       *wlturbo.Display
	c       *loopConn
	surface *core.Surface
	ptr     *core.Pointer
	output  *core.Output
	vp      *viewporter.WpViewport
	shape   *cursorshape.WpCursorShapeDevice
	scale   *fractionalscale.WpFractionalScale
	vpr     *viewporter.WpViewporter
}

func newLoopEnv(tb testing.TB) *loopEnv {
	tb.Helper()
	c := &loopConn{pattern: make([]byte, 12)}
	d, err := wlturbo.ConnectFromConn(c)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { d.Close() })
	e := &loopEnv{d: d, c: c}
	reg := func(p wlturbo.Proxy) {
		p.SetID(d.AllocateID())
		d.Context().Register(p)
	}
	e.surface = core.NewSurface(d.Context())
	e.ptr = core.NewPointer(d.Context())
	e.output = core.NewOutput(d.Context())
	e.vp = viewporter.NewWpViewport(d.Context())
	e.shape = cursorshape.NewWpCursorShapeDevice(d.Context())
	e.scale = fractionalscale.NewWpFractionalScale(d.Context())
	e.vpr = viewporter.NewWpViewporter(d.Context())
	for _, p := range []wlturbo.Proxy{e.surface, e.ptr, e.output, e.vp, e.shape, e.scale, e.vpr} {
		reg(p)
	}
	return e
}

// perfFrame encodes one event with 32-bit words as its whole body.
func perfFrame(id uint32, op uint16, words ...uint32) []byte {
	b := make([]byte, 8+4*len(words))
	binary.LittleEndian.PutUint32(b, id)
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b))<<16|uint32(op))
	for i, v := range words {
		binary.LittleEndian.PutUint32(b[8+i*4:], v)
	}
	return b
}

// perfStringFrame encodes an event whose only argument is a string.
func perfStringFrame(id uint32, op uint16, s string) []byte {
	n := len(s) + 1
	padded := (n + 3) &^ 3
	b := make([]byte, 8+4+padded)
	binary.LittleEndian.PutUint32(b, id)
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b))<<16|uint32(op))
	binary.LittleEndian.PutUint32(b[8:], uint32(n))
	copy(b[12:], s)
	return b
}

// numericRequests are numeric requests with values that do NOT hit the
// runtime's small-integer boxing table: this is the realistic cost.
func (e *loopEnv) numericRequests() []struct {
	name string
	args int // number of numeric arguments
	run  func() error
} {
	x, w := wlturbo.NewFixed(10.5), wlturbo.NewFixed(1920)
	return []struct {
		name string
		args int
		run  func() error
	}{
		{"wp_viewport.set_source", 4, func() error { return e.vp.SetSource(x, x, w, w) }},
		{"wp_viewport.set_destination", 2, func() error { return e.vp.SetDestination(1920, 1080) }},
		{"wp_cursor_shape_device_v1.set_shape", 2, func() error { return e.shape.SetShape(123456, cursorshape.SHAPE_POINTER) }},
		{"wl_surface.damage_buffer", 4, func() error { return e.surface.DamageBuffer(0, 0, 1920, 1080) }},
		{"wl_surface.set_buffer_scale", 1, func() error { return e.surface.SetBufferScale(1000) }},
	}
}

// TestRequestAllocations pins the request-side allocation contract.
func TestRequestAllocations(t *testing.T) {
	e := newLoopEnv(t)
	run := func(name string, f func() error) float64 {
		var err error
		n := testing.AllocsPerRun(500, func() { err = f() })
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return n
	}
	// Requests without arguments, and numeric requests whose values are
	// below 256 (the runtime's static boxing table), are allocation-free.
	// Both must stay so, whatever the generator does with arguments.
	for _, tc := range []struct {
		name string
		f    func() error
	}{
		{"wl_surface.commit", e.surface.Commit},
		{"wl_surface.set_buffer_scale(2)", func() error { return e.surface.SetBufferScale(2) }},
		{"wp_viewport.set_destination(64,32)", func() error { return e.vp.SetDestination(64, 32) }},
		{"wp_cursor_shape_device_v1.set_shape(small)", func() error { return e.shape.SetShape(7, cursorshape.SHAPE_DEFAULT) }},
	} {
		if n := run(tc.name, tc.f); n != 0 {
			t.Errorf("%s: %v allocs/op, want 0", tc.name, n)
		}
	}
	// Realistic values (outside the runtime's small-integer boxing table)
	// travel as typed wl.Arg values and must not allocate either.
	for _, tc := range e.numericRequests() {
		if n := run(tc.name, tc.run); n != 0 {
			t.Errorf("%s: %v allocs/op, want 0 (%d numeric args)", tc.name, n, tc.args)
		}
	}
}

// TestNumericEventDispatchAllocationFree pins the event side: framing,
// signature validation, pooled Event and typed handler dispatch for numeric
// arguments must not allocate.
func TestNumericEventDispatchAllocationFree(t *testing.T) {
	// One environment per case: loopConn refills a whole receive chunk, so
	// frames of a previous pattern would still be buffered.
	for _, tc := range []struct {
		name  string
		setup func(e *loopEnv) (frame []byte, observed func() bool)
	}{
		{"wp_fractional_scale_v1.preferred_scale", func(e *loopEnv) ([]byte, func() bool) {
			var scale uint32
			e.scale.OnPreferredScale(func(s uint32) { scale = s })
			return perfFrame(e.scale.ID(), 0, 180), func() bool { return scale == 180 }
		}},
		{"wl_pointer.motion", func(e *loopEnv) ([]byte, func() bool) {
			var tm uint32
			var fx, fy wlturbo.Fixed
			e.ptr.OnMotion(func(t uint32, x, y wlturbo.Fixed) { tm, fx, fy = t, x, y })
			return perfFrame(e.ptr.ID(), 2, 1234567, 300*256, 0xffffff00), func() bool { return tm == 1234567 && fx == 300*256 && fy == -256 }
		}},
		{"wl_surface.preferred_buffer_scale", func(e *loopEnv) ([]byte, func() bool) {
			var factor int32
			e.surface.OnPreferredBufferScale(func(f int32) { factor = f })
			return perfFrame(e.surface.ID(), 2, 3), func() bool { return factor == 3 }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newLoopEnv(t)
			f, observed := tc.setup(e)
			e.c.pattern = f
			var derr error
			n := testing.AllocsPerRun(1000, func() {
				if err := e.d.Dispatch(); err != nil {
					derr = err
				}
			})
			if derr != nil {
				t.Fatal(derr)
			}
			if !observed() {
				t.Fatal("handler did not observe the event")
			}
			if n != 0 {
				t.Errorf("%v allocs/op, want 0", n)
			}
		})
	}
}

// TestNecessaryAllocations documents allocations that are inherent to the
// operation. They are bounded so they cannot silently grow, but they are not
// expected to reach zero.
func TestNecessaryAllocations(t *testing.T) {
	e := newLoopEnv(t)

	// A received string is copied out of the receive buffer: one allocation.
	var name string
	e.output.OnName(func(s string) { name = s })
	e.c.pattern = perfStringFrame(e.output.ID(), 4, "HDMI-A-1")
	n := testing.AllocsPerRun(1000, func() { _ = e.d.Dispatch() })
	if name != "HDMI-A-1" {
		t.Fatalf("string event not delivered: %q", name)
	}
	if n > 1 {
		t.Errorf("wl_output.name: %v allocs/op, want <= 1 (the string copy)", n)
	}

	// A new_id request creates a proxy and registers it in two maps; that is
	// the object-creation cost, reported rather than asserted to be zero.
	// The IDs are never released in this loopback, so keep the run short.
	n = testing.AllocsPerRun(50, func() {
		if _, err := e.vpr.GetViewport(e.surface); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("wp_viewporter.get_viewport (object creation): %v allocs/op", n)
	if n < 1 {
		t.Errorf("get_viewport allocated %v; a child proxy must be created", n)
	}
}

// BenchmarkRequests measures generated numeric requests with small and
// realistic values; both are allocation-free.
func BenchmarkRequests(b *testing.B) {
	e := newLoopEnv(b)
	b.Run("no_args/wl_surface.commit", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if err := e.surface.Commit(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("small_values/wl_surface.set_buffer_scale", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if err := e.surface.SetBufferScale(2); err != nil {
				b.Fatal(err)
			}
		}
	})
	for _, tc := range e.numericRequests() {
		b.Run("realistic_values/"+tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := tc.run(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkEventDispatch measures typed numeric event dispatch through the
// generated Dispatch methods, without socket scheduling.
func BenchmarkEventDispatch(b *testing.B) {
	e := newLoopEnv(b)
	var sink uint32
	e.scale.OnPreferredScale(func(s uint32) { sink += s })
	e.ptr.OnMotion(func(tm uint32, x, y wlturbo.Fixed) { sink += tm + uint32(x) + uint32(y) })
	for _, tc := range []struct {
		name  string
		frame []byte
	}{
		{"wp_fractional_scale_v1.preferred_scale", perfFrame(e.scale.ID(), 0, 180)},
		{"wl_pointer.motion", perfFrame(e.ptr.ID(), 2, 1234567, 300*256, 400*256)},
	} {
		b.Run("numeric/"+tc.name, func(b *testing.B) {
			e.c.pattern = tc.frame
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := e.d.Dispatch(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	_ = sink
}

// BenchmarkNecessaryAllocations reports operations whose allocations cannot be
// removed by the generator: string copies, object creation and received FDs.
func BenchmarkNecessaryAllocations(b *testing.B) {
	b.Run("string_event/wl_output.name", func(b *testing.B) {
		e := newLoopEnv(b)
		var name string
		e.output.OnName(func(s string) { name = s })
		e.c.pattern = perfStringFrame(e.output.ID(), 4, "HDMI-A-1")
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if err := e.d.Dispatch(); err != nil {
				b.Fatal(err)
			}
		}
		_ = name
	})
	b.Run("new_id/wp_viewporter.get_viewport", func(b *testing.B) {
		e := newLoopEnv(b)
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := e.vpr.GetViewport(e.surface); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("received_fd/wl_keyboard.keymap", func(b *testing.B) {
		fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
		if err != nil {
			b.Fatal(err)
		}
		fa, fb := os.NewFile(uintptr(fds[0]), "a"), os.NewFile(uintptr(fds[1]), "b")
		ca, err := net.FileConn(fa)
		fa.Close()
		if err != nil {
			b.Fatal(err)
		}
		cb, err := net.FileConn(fb)
		fb.Close()
		if err != nil {
			b.Fatal(err)
		}
		defer cb.Close()
		peer := cb.(*net.UnixConn)
		d, err := wlturbo.ConnectFromConn(ca)
		if err != nil {
			b.Fatal(err)
		}
		defer d.Close()
		kb := core.NewKeyboard(d.Context())
		kb.SetID(d.AllocateID())
		d.Context().Register(kb)
		kb.OnKeymap(func(format uint32, fd *wlturbo.OwnedFD, size uint32) {})
		// The descriptor sent each iteration; the receiver closes its copy.
		null, err := os.Open(os.DevNull)
		if err != nil {
			b.Fatal(err)
		}
		defer null.Close()
		msg := perfFrame(kb.ID(), 0, 1, 4096)
		rights := unix.UnixRights(int(null.Fd()))
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, _, err := peer.WriteMsgUnix(msg, rights, nil); err != nil {
				b.Fatal(err)
			}
			if err := d.Dispatch(); err != nil {
				b.Fatal(err)
			}
		}
	})
}
