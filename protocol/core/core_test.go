//go:build linux

package core_test

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/bnema/wlturbo"
	"github.com/bnema/wlturbo/protocol/core"
	"golang.org/x/sys/unix"
)

func pair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	f, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	a := os.NewFile(uintptr(f[0]), "a")
	b := os.NewFile(uintptr(f[1]), "b")
	ca, e := net.FileConn(a)
	a.Close()
	if e != nil {
		t.Fatal(e)
	}
	cb, e := net.FileConn(b)
	b.Close()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ca.Close(); cb.Close() })
	return ca.(*net.UnixConn), cb.(*net.UnixConn)
}
func frame(id uint32, op uint16, body ...uint32) []byte {
	p := make([]byte, 8+4*len(body))
	binary.LittleEndian.PutUint32(p, id)
	binary.LittleEndian.PutUint32(p[4:], uint32(len(p))<<16|uint32(op))
	for i, v := range body {
		binary.LittleEndian.PutUint32(p[8+i*4:], v)
	}
	return p
}
func receiveRequest(t *testing.T, p *net.UnixConn) {
	t.Helper()
	p.SetReadDeadline(testDeadline())
	b := make([]byte, 256)
	if _, e := p.Read(b); e != nil {
		t.Fatal(e)
	}
}
func testDeadline() (d time.Time) { return time.Now().Add(2 * time.Second) }
func send(t *testing.T, p *net.UnixConn, msg []byte, fd ...int) {
	t.Helper()
	var oob []byte
	if len(fd) > 0 {
		oob = unix.UnixRights(fd...)
	}
	if _, _, e := p.WriteMsgUnix(msg, oob, nil); e != nil {
		t.Fatal(e)
	}
}
func TestGeneratedCoreOverSocketpair(t *testing.T) {
	c, p := pair(t)
	d, err := wlturbo.ConnectFromConn(c)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	receiveRequest(t, p) // get_registry
	seat := core.NewSeat(d.Context())
	if err := d.Registry().Bind(1, core.SeatInterface, 1, seat); err != nil {
		t.Fatal(err)
	}
	receiveRequest(t, p)
	kb, err := seat.GetKeyboard()
	if err != nil {
		t.Fatal(err)
	}
	receiveRequest(t, p)
	var key uint32
	kb.OnKey(func(_, _, k, _ uint32) { key = k })
	send(t, p, frame(kb.ID(), 3, 1, 2, 42, 1))
	if err := d.Dispatch(); err != nil {
		t.Fatal(err)
	}
	if key != 42 {
		t.Fatalf("key=%d", key)
	}
	compositor := core.NewCompositor(d.Context())
	if err := d.Registry().Bind(2, core.CompositorInterface, 1, compositor); err != nil {
		t.Fatal(err)
	}
	receiveRequest(t, p)
	surface, err := compositor.CreateSurface()
	if err != nil {
		t.Fatal(err)
	}
	receiveRequest(t, p)
	callback, err := surface.Frame()
	if err != nil {
		t.Fatal(err)
	}
	receiveRequest(t, p)
	var done uint32
	callback.OnDone(func(ms uint32) { done = ms })
	send(t, p, frame(callback.ID(), 0, 123))
	if err := d.Dispatch(); err != nil {
		t.Fatal(err)
	}
	if done != 123 {
		t.Fatalf("done=%d", done)
	}
	send(t, p, frame(callback.ID(), 0, 124))
	if err := d.Dispatch(); !errors.Is(err, wlturbo.ErrUnknownObject) {
		t.Fatalf("destroyed callback: %v", err)
	}
}
func TestKeymapFDNoHandlerAndMultipleHandlers(t *testing.T) {
	c, p := pair(t)
	d, e := wlturbo.ConnectFromConn(c)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	receiveRequest(t, p)
	kb := core.NewKeyboard(d.Context())
	kb.SetID(d.AllocateID())
	d.Context().Register(kb)
	r, w, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	send(t, p, frame(kb.ID(), 0, 1, 8), int(w.Fd()))
	w.Close()
	if e := d.Dispatch(); e != nil {
		t.Fatal(e)
	}
	buf := make([]byte, 1)
	if n, e := r.Read(buf); n != 0 || e != io.EOF {
		t.Fatalf("unclaimed FD n=%d err=%v", n, e)
	}
	r2, w2, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer r2.Close()
	received := 0
	kb.OnKeymap(func(_ uint32, fd *wlturbo.OwnedFD, _ uint32) {
		if fd == nil {
			t.Fatal("first FD missing")
		}
		n, e := fd.Take()
		if e != nil {
			t.Fatal(e)
		}
		received++
		unix.Close(n)
	})
	kb.OnKeymap(func(_ uint32, fd *wlturbo.OwnedFD, _ uint32) {
		if fd != nil {
			t.Fatal("duplicated owner")
		}
		received++
	})
	send(t, p, frame(kb.ID(), 0, 1, 8), int(w2.Fd()))
	w2.Close()
	if e := d.Dispatch(); e != nil {
		t.Fatal(e)
	}
	if received != 2 {
		t.Fatalf("handlers=%d", received)
	}
}
func TestBufferReleaseAndOfferChild(t *testing.T) {
	c, p := pair(t)
	d, e := wlturbo.ConnectFromConn(c)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	receiveRequest(t, p)
	b := core.NewBuffer(d.Context())
	b.SetID(d.AllocateID())
	d.Context().Register(b)
	releases := 0
	b.OnRelease(func() { releases++ })
	send(t, p, frame(b.ID(), 0))
	if e := d.Dispatch(); e != nil {
		t.Fatal(e)
	}
	if releases != 1 {
		t.Fatal("release not dispatched")
	}
	device := core.NewDataDevice(d.Context())
	device.SetID(d.AllocateID())
	d.Context().Register(device)
	var offer *core.DataOffer
	device.OnDataOffer(func(o *core.DataOffer) { offer = o })
	send(t, p, frame(device.ID(), 0, 0xff000001))
	if e := d.Dispatch(); e != nil {
		t.Fatal(e)
	}
	if offer == nil || offer.ID() != 0xff000001 {
		t.Fatalf("offer=%v", offer)
	}
}

func TestMissingKeymapDescriptor(t *testing.T) {
	c, p := pair(t)
	d, e := wlturbo.ConnectFromConn(c)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	receiveRequest(t, p)
	kb := core.NewKeyboard(d.Context())
	kb.SetID(d.AllocateID())
	d.Context().Register(kb)
	send(t, p, frame(kb.ID(), 0, 1, 8))
	if e := d.Dispatch(); !errors.Is(e, wlturbo.ErrMalformedFrame) {
		t.Fatalf("missing FD: %v", e)
	}
}

func TestInterleavedFDAndPlainMessages(t *testing.T) {
	c, p := pair(t)
	d, e := wlturbo.ConnectFromConn(c)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	receiveRequest(t, p)
	kb := core.NewKeyboard(d.Context())
	kb.SetID(d.AllocateID())
	d.Context().Register(kb)
	calls := 0
	kb.OnKeymap(func(_ uint32, fd *wlturbo.OwnedFD, _ uint32) {
		if fd == nil {
			t.Fatal("FD not assigned")
		}
		_ = fd.Close()
		calls++
	})
	kb.OnKey(func(_, _, key, _ uint32) {
		if key != 33 {
			t.Errorf("key=%d", key)
		}
		calls++
	})
	r, w, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	send(t, p, frame(kb.ID(), 0, 1, 8), int(w.Fd()))
	w.Close()
	send(t, p, frame(kb.ID(), 3, 1, 2, 33, 1))
	if e := d.Dispatch(); e != nil {
		t.Fatal(e)
	}
	if e := d.Dispatch(); e != nil {
		t.Fatal(e)
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
}
func TestMalformedGeneratedPayload(t *testing.T) {
	c, p := pair(t)
	d, e := wlturbo.ConnectFromConn(c)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	receiveRequest(t, p)
	kb := core.NewKeyboard(d.Context())
	kb.SetID(d.AllocateID())
	d.Context().Register(kb)
	send(t, p, frame(kb.ID(), 3, 1, 2))
	if e := d.Dispatch(); !errors.Is(e, wlturbo.ErrMalformedFrame) {
		t.Fatalf("truncated key: %v", e)
	}
}

func TestServerChildEventsAfterOffer(t *testing.T) {
	c, p := pair(t)
	d, e := wlturbo.ConnectFromConn(c)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	receiveRequest(t, p)
	device := core.NewDataDevice(d.Context())
	device.SetID(d.AllocateID())
	d.Context().Register(device)
	const offerID = 0xff000011
	var offer *core.DataOffer
	device.OnDataOffer(func(o *core.DataOffer) { offer = o })
	send(t, p, frame(device.ID(), 0, offerID))
	if e := d.Dispatch(); e != nil {
		t.Fatal(e)
	}
	if offer == nil {
		t.Fatal("missing concrete child")
	}
	got := ""
	offer.OnOffer(func(mime string) { got = mime })
	payload := append(frame(offerID, 0), 4, 0, 0, 0, 'a', 'b', 'c', 0)
	binary.LittleEndian.PutUint32(payload[4:], uint32(len(payload))<<16)
	send(t, p, payload)
	if e := d.Dispatch(); e != nil {
		t.Fatal(e)
	}
	if got != "abc" {
		t.Fatalf("typed child event: %q", got)
	}
}

func TestRegistryBootstrapAnnouncements(t *testing.T) {
	c, p := pair(t)
	d, e := wlturbo.ConnectFromConn(c)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	receiveRequest(t, p)
	var got wlturbo.Global
	d.Registry().AddHandler("wl_compositor", func(r *wlturbo.Registry, n, v uint32) { got, _ = r.FindGlobalByName(n) })
	name := "wl_compositor"
	body := make([]byte, 4+4+((len(name)+1+3)&^3)+4)
	binary.LittleEndian.PutUint32(body, 17)
	binary.LittleEndian.PutUint32(body[4:], uint32(len(name)+1))
	copy(body[8:], name)
	binary.LittleEndian.PutUint32(body[len(body)-4:], 7)
	msg := append(frame(d.Registry().ID(), 0), body...)
	binary.LittleEndian.PutUint32(msg[4:], uint32(len(msg))<<16)
	send(t, p, msg)
	if e := d.Dispatch(); e != nil {
		t.Fatal(e)
	}
	if got.Name != 17 || got.Interface != name || got.Version != 7 {
		t.Fatalf("global: %+v", got)
	}
	send(t, p, frame(d.Registry().ID(), 1, 17))
	if e := d.Dispatch(); e != nil {
		t.Fatal(e)
	}
	if _, ok := d.Registry().FindGlobalByName(17); ok {
		t.Fatal("global not removed")
	}
	send(t, p, frame(d.Registry().ID(), 2))
	if e := d.Dispatch(); !errors.Is(e, wlturbo.ErrUnknownOpcode) {
		t.Fatalf("unknown registry opcode: %v", e)
	}
}
func TestUnclaimedFirstHandlerFDClosed(t *testing.T) {
	c, p := pair(t)
	d, e := wlturbo.ConnectFromConn(c)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	receiveRequest(t, p)
	kb := core.NewKeyboard(d.Context())
	kb.SetID(d.AllocateID())
	d.Context().Register(kb)
	kb.OnKeymap(func(_ uint32, fd *wlturbo.OwnedFD, _ uint32) {
		if fd == nil {
			t.Fatal("missing first owner")
		}
	})
	kb.OnKeymap(func(_ uint32, fd *wlturbo.OwnedFD, _ uint32) {
		if fd != nil {
			t.Fatal("duplicate owner")
		}
	})
	r, w, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	send(t, p, frame(kb.ID(), 0, 1, 8), int(w.Fd()))
	w.Close()
	if e := d.Dispatch(); e != nil {
		t.Fatal(e)
	}
	if e := r.SetReadDeadline(testDeadline()); e != nil {
		t.Fatal(e)
	}
	var b [1]byte
	if n, e := r.Read(b[:]); n != 0 || e != io.EOF {
		t.Fatalf("unclaimed FD not closed: %d %v", n, e)
	}
}
