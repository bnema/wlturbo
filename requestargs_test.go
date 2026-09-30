package wlturbo

import (
	"bytes"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// captureConn records every write so tests can compare wire bytes.
type captureConn struct {
	mu     sync.Mutex
	writes [][]byte
}

func (c *captureConn) Read([]byte) (int, error) { return 0, net.ErrClosed }
func (c *captureConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writes = append(c.writes, append([]byte(nil), p...))
	return len(p), nil
}
func (c *captureConn) Close() error                     { return nil }
func (c *captureConn) LocalAddr() net.Addr              { return testAddr{} }
func (c *captureConn) RemoteAddr() net.Addr             { return testAddr{} }
func (c *captureConn) SetDeadline(time.Time) error      { return nil }
func (c *captureConn) SetReadDeadline(time.Time) error  { return nil }
func (c *captureConn) SetWriteDeadline(time.Time) error { return nil }

func (c *captureConn) last(t *testing.T) []byte {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.writes) == 0 {
		t.Fatal("nothing was written")
	}
	return c.writes[len(c.writes)-1]
}

func (c *captureConn) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.writes)
}

func newCaptureEnv() (*Display, *captureConn, *BaseProxy) {
	c := &captureConn{}
	d := newDisplay(c)
	parent := &BaseProxy{id: d.AllocateID(), context: d.context, version: 3}
	d.context.Register(parent)
	return d, c, parent
}

// RequestArgs and Request must put identical bytes on the wire for every
// argument kind, including padding boundaries and nil objects.
func TestRequestArgsMatchesBoxedRequest(t *testing.T) {
	d, c, parent := newCaptureEnv()
	obj := &BaseProxy{id: 77, context: d.context}
	var nilObj Object
	cases := []struct {
		name  string
		boxed []interface{}
		typed []Arg
	}{
		{"none", nil, nil},
		{"words", []interface{}{uint32(0xdeadbeef), int32(-5), Fixed(-300)},
			[]Arg{ArgUint(0xdeadbeef), ArgInt(-5), ArgFixed(-300)}},
		{"string_pad0", []interface{}{"abc"}, []Arg{ArgString("abc")}},
		{"string_pad3", []interface{}{"a"}, []Arg{ArgString("a")}},
		{"string_empty", []interface{}{""}, []Arg{ArgString("")}},
		{"array_pad", []interface{}{[]byte{1, 2, 3, 4, 5}}, []Arg{ArgArray([]byte{1, 2, 3, 4, 5})}},
		{"array_nil", []interface{}{[]byte(nil)}, []Arg{ArgArray(nil)}},
		{"object", []interface{}{Object(obj)}, []Arg{ArgObject(obj)}},
		{"nil_object", []interface{}{nilObj}, []Arg{ArgObject(nil)}},
		{"mixed", []interface{}{uint32(9), "hello", Object(obj), int32(2), []byte("xy"), Fixed(256)},
			[]Arg{ArgUint(9), ArgString("hello"), ArgObject(obj), ArgInt(2), ArgArray([]byte("xy")), ArgFixed(256)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := d.context.Request(Request{Proxy: parent, Opcode: 5, Name: "x.r"}, tc.boxed...); err != nil {
				t.Fatal(err)
			}
			want := c.last(t)
			if err := d.context.RequestArgs(Request{Proxy: parent, Opcode: 5, Name: "x.r"}, tc.typed...); err != nil {
				t.Fatal(err)
			}
			got := c.last(t)
			if !bytes.Equal(got, want) {
				t.Fatalf("typed %x != boxed %x", got, want)
			}
			if len(got)%4 != 0 {
				t.Fatalf("unaligned message: %d", len(got))
			}
		})
	}
}

// Numeric words are written little-endian (native wire order on supported
// targets) after a header carrying object, opcode and size.
func TestRequestArgsWireLayout(t *testing.T) {
	d, c, parent := newCaptureEnv()
	if err := d.context.RequestArgs(Request{Proxy: parent, Opcode: 2, Name: "x.r"}, ArgUint(1), ArgInt(-1), ArgString("hi")); err != nil {
		t.Fatal(err)
	}
	want := []byte{
		byte(parent.ID()), 0, 0, 0, 2, 0, 24, 0,
		1, 0, 0, 0,
		0xff, 0xff, 0xff, 0xff,
		3, 0, 0, 0, 'h', 'i', 0, 0,
	}
	if got := c.last(t); !bytes.Equal(got, want) {
		t.Fatalf("got %x want %x", got, want)
	}
}

func TestRequestArgsMalformed(t *testing.T) {
	d, c, parent := newCaptureEnv()
	child := &BaseProxy{context: d.context}
	tooBig := strings.Repeat("a", 0xffff)
	for _, tc := range []struct {
		name string
		args []Arg
	}{
		{"zero_arg", []Arg{ArgUint(1), {}}},
		{"string_too_long", []Arg{ArgString(tooBig)}},
		{"array_too_long", []Arg{ArgArray(make([]byte, 0xffff))}},
		{"total_too_large", []Arg{ArgArray(make([]byte, 0xfff0)), ArgArray(make([]byte, 0x20))}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := c.count()
			args := append([]Arg{ArgObject(child)}, tc.args...)
			if err := d.context.RequestArgs(Request{Proxy: parent, Opcode: 1, Name: "x.bad", Child: child}, args...); err == nil {
				t.Fatal("malformed request succeeded")
			}
			if c.count() != before {
				t.Fatal("bytes written for a malformed request")
			}
			if _, ok := d.objects.Load(child.ID()); ok {
				t.Fatal("child left registered after failed send")
			}
		})
	}
	// The largest 4-byte aligned message (0xfffc) still goes through.
	if err := d.context.RequestArgs(Request{Proxy: parent, Opcode: 1, Name: "x.ok"}, ArgArray(make([]byte, 0xfffc-HeaderSize-4))); err != nil {
		t.Fatalf("largest array: %v", err)
	}
	if got := c.last(t); len(got) != 0xfffc {
		t.Fatalf("size = %d", len(got))
	}
}

func TestRequestArgsLifecycle(t *testing.T) {
	d, c, parent := newCaptureEnv()

	// Version, child allocation and inheritance.
	late := &BaseProxy{context: d.context}
	err := d.context.RequestArgs(Request{Proxy: parent, Opcode: 1, Name: "x.late", Since: 4, Child: late}, ArgObject(late))
	if !errors.Is(err, ErrVersionTooLow) {
		t.Fatalf("since=4 on v3 = %v, want ErrVersionTooLow", err)
	}
	if late.ID() != 0 || c.count() != 0 {
		t.Fatal("refused request allocated a child or wrote")
	}
	child := &BaseProxy{context: d.context}
	if err := d.context.RequestArgs(Request{Proxy: parent, Opcode: 0, Name: "x.make", Child: child}, ArgObject(child)); err != nil {
		t.Fatal(err)
	}
	if child.ID() == 0 || child.Version() != 3 {
		t.Fatalf("child id=%d version=%d", child.ID(), child.Version())
	}
	if _, ok := d.objects.Load(child.ID()); !ok {
		t.Fatal("child not registered")
	}

	// Destructor: claimed exactly once; later requests fail without writing.
	if err := d.context.RequestArgs(Request{Proxy: parent, Opcode: 9, Name: "x.destroy", Destructor: true}); err != nil {
		t.Fatal(err)
	}
	n := c.count()
	if err := d.context.RequestArgs(Request{Proxy: parent, Opcode: 9, Name: "x.destroy", Destructor: true}); err == nil {
		t.Fatal("second destructor succeeded")
	}
	if err := d.context.RequestArgs(Request{Proxy: parent, Opcode: 1, Name: "x.after"}, ArgUint(1)); err == nil {
		t.Fatal("request after destructor succeeded")
	}
	if c.count() != n {
		t.Fatal("bytes written after destructor")
	}

	// A marshal failure leaves a destructor's proxy registered.
	p2 := &BaseProxy{id: d.AllocateID(), context: d.context, version: 1}
	d.context.Register(p2)
	if err := d.context.RequestArgs(Request{Proxy: p2, Opcode: 9, Name: "x.destroy", Destructor: true}, Arg{}); err == nil {
		t.Fatal("zero Arg accepted")
	}
	if err := d.context.CheckProxy(p2); err != nil {
		t.Fatalf("proxy lost after marshal error: %v", err)
	}

	// Foreign and closed contexts.
	other := &BaseProxy{id: 5, context: NewContext(d)}
	if err := d.context.RequestArgs(Request{Proxy: other, Name: "x.r"}); err == nil {
		t.Fatal("foreign proxy accepted")
	}
	d.context.closed.Store(true)
	if err := d.context.RequestArgs(Request{Proxy: p2, Name: "x.r"}); err == nil {
		t.Fatal("closed context accepted")
	}
}

// Descriptors are closed after a successful send and stay with the caller on
// failure, exactly as with Request.
func TestRequestArgsFDOwnership(t *testing.T) {
	sp, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	fa, fb := os.NewFile(uintptr(sp[0]), "a"), os.NewFile(uintptr(sp[1]), "b")
	ca, err := net.FileConn(fa)
	fa.Close()
	if err != nil {
		t.Fatal(err)
	}
	cb, err := net.FileConn(fb)
	fb.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer cb.Close()
	d, err := ConnectFromConn(ca)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	parent := &BaseProxy{id: d.AllocateID(), context: d.Context(), version: 1}
	d.Context().Register(parent)

	open := func() int {
		f, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatal(err)
		}
		fd, err := unix.Dup(int(f.Fd()))
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		return fd
	}
	isOpen := func(fd int) bool { _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); return err == nil }

	fd := open()
	if err := d.Context().RequestArgs(Request{Proxy: parent, Opcode: 0, Name: "x.fd", FDs: []int{fd}}, ArgString("m"), ArgFD(), ArgUint(4)); err != nil {
		t.Fatal(err)
	}
	if isOpen(fd) {
		t.Fatal("descriptor not closed after successful send")
	}
	body := make([]byte, 8+8+4)
	if n, oob, err := readWithOOB(cb.(*net.UnixConn), body); err != nil || n != len(body) || oob == 0 {
		t.Fatalf("peer read n=%d oob=%d err=%v", n, oob, err)
	}

	fd = open()
	defer unix.Close(fd)
	if err := d.Context().RequestArgs(Request{Proxy: parent, Opcode: 0, Name: "x.fd", FDs: []int{fd}}, ArgFD(), Arg{}); err == nil {
		t.Fatal("zero Arg accepted")
	}
	if !isOpen(fd) {
		t.Fatal("descriptor closed after failed send")
	}
}

func readWithOOB(c *net.UnixConn, buf []byte) (n, oobn int, err error) {
	oob := make([]byte, unix.CmsgSpace(4))
	n, oobn, _, _, err = c.ReadMsgUnix(buf, oob)
	return
}

// The number of ArgFD markers must equal len(FDs). A mismatch is rejected
// before the child is allocated, the guard runs or anything is written; the
// caller keeps its descriptors and a destructor proxy stays unclaimed.
func TestRequestArgsFDMarkerMismatch(t *testing.T) {
	d, c, parent := newCaptureEnv()
	fd, err := unix.Open(os.DevNull, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	isOpen := func() bool { _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); return err == nil }

	for _, tc := range []struct {
		name       string
		fds        []int
		args       []Arg
		destructor bool
	}{
		{"fd_without_marker", []int{fd}, []Arg{ArgUint(1)}, false},
		{"fd_without_any_args", []int{fd}, nil, false},
		{"marker_without_fd", nil, []Arg{ArgFD(), ArgUint(1)}, false},
		{"two_markers_one_fd", []int{fd}, []Arg{ArgFD(), ArgFD()}, false},
		{"one_marker_two_fds", []int{fd, fd}, []Arg{ArgFD()}, false},
		{"destructor_fd_without_marker", []int{fd}, nil, true},
		{"destructor_marker_without_fd", nil, []Arg{ArgFD()}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := c.count()
			child := &BaseProxy{context: d.context}
			args := tc.args
			r := Request{Proxy: parent, Opcode: 1, Name: "x.fd", FDs: tc.fds, Destructor: tc.destructor}
			if !tc.destructor {
				r.Child = child
				args = append([]Arg{ArgObject(child)}, args...)
			}
			if err := d.context.RequestArgs(r, args...); err == nil {
				t.Fatal("mismatched fd markers accepted")
			}
			if c.count() != before {
				t.Fatal("bytes written")
			}
			if child.ID() != 0 {
				t.Fatal("child allocated")
			}
			if !isOpen() {
				t.Fatal("caller descriptor closed")
			}
			if err := d.context.CheckProxy(parent); err != nil {
				t.Fatalf("proxy claimed or lost: %v", err)
			}
		})
	}

	// Legacy semantics are unchanged: Request does not count placeholders.
	if err := d.context.Request(Request{Proxy: parent, Opcode: 1, Name: "x.legacy"}, uintptr(3)); err != nil {
		t.Fatalf("legacy uintptr placeholder without FDs: %v", err)
	}
}

// A typed request racing a destructor on the same proxy either is written
// before the destructor or fails without writing; nothing follows the
// destructor on the wire.
func TestRequestArgsConcurrentWithDestructor(t *testing.T) {
	for i := 0; i < 50; i++ {
		d, c, parent := newCaptureEnv()
		var wg sync.WaitGroup
		var ok atomic.Int32
		for g := 0; g < 4; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if d.context.RequestArgs(Request{Proxy: parent, Opcode: 1, Name: "x.r"}, ArgUint(1)) == nil {
					ok.Add(1)
				}
			}()
		}
		var destroyed atomic.Int32
		for g := 0; g < 2; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if d.context.RequestArgs(Request{Proxy: parent, Opcode: 9, Name: "x.destroy", Destructor: true}) == nil {
					destroyed.Add(1)
				}
			}()
		}
		wg.Wait()
		if destroyed.Load() != 1 {
			t.Fatalf("destructor sent %d times, want exactly 1", destroyed.Load())
		}
		c.mu.Lock()
		writes := c.writes
		c.mu.Unlock()
		if int32(len(writes)) != ok.Load()+1 {
			t.Fatalf("%d writes for %d requests + destructor", len(writes), ok.Load())
		}
		for j, w := range writes {
			isDestructor := w[4] == 9
			if isDestructor != (j == len(writes)-1) {
				t.Fatalf("destructor not last on the wire: write %d of %d", j, len(writes))
			}
		}
	}
}
