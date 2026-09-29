//go:build linux

package protocol_test

import (
	"encoding/binary"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bnema/wlturbo"
	"github.com/bnema/wlturbo/protocol/core"
	"github.com/bnema/wlturbo/protocol/cursorshape"
	"github.com/bnema/wlturbo/protocol/drmsyncobj"
	"github.com/bnema/wlturbo/protocol/fractionalscale"
	"github.com/bnema/wlturbo/protocol/linuxdmabuf"
	"github.com/bnema/wlturbo/protocol/textinput"
	"github.com/bnema/wlturbo/protocol/viewporter"
	"github.com/bnema/wlturbo/protocol/xdgshell"
	"golang.org/x/sys/unix"
)

func pair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	fds, e := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if e != nil {
		t.Fatal(e)
	}
	a := os.NewFile(uintptr(fds[0]), "a")
	b := os.NewFile(uintptr(fds[1]), "b")
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
func msg(id uint32, op uint16, body ...uint32) []byte {
	b := make([]byte, 8+4*len(body))
	binary.NativeEndian.PutUint32(b, id)
	binary.NativeEndian.PutUint32(b[4:], uint32(len(b))<<16|uint32(op))
	for i, v := range body {
		binary.NativeEndian.PutUint32(b[8+i*4:], v)
	}
	return b
}
func payload(id uint32, op uint16, b ...byte) []byte {
	p := make([]byte, 8+len(b))
	copy(p, msg(id, op))
	copy(p[8:], b)
	binary.NativeEndian.PutUint32(p[4:], uint32(len(p))<<16|uint32(op))
	return p
}
func arr(b []byte) []byte {
	out := make([]byte, 4+(len(b)+3)&^3)
	binary.NativeEndian.PutUint32(out, uint32(len(b)))
	copy(out[4:], b)
	return out
}
func str(s string) []byte { return arr(append([]byte(s), 0)) }
func send(t *testing.T, p *net.UnixConn, b []byte, fd ...int) {
	t.Helper()
	var rights []byte
	if len(fd) > 0 {
		rights = unix.UnixRights(fd...)
	}
	if _, _, e := p.WriteMsgUnix(b, rights, nil); e != nil {
		t.Fatal(e)
	}
}
func request(t *testing.T, p *net.UnixConn) (uint32, uint16, []byte) {
	t.Helper()
	p.SetReadDeadline(time.Now().Add(2 * time.Second))
	b := make([]byte, 4096)
	n, e := p.Read(b)
	if e != nil {
		t.Fatal(e)
	}
	if n < 8 {
		t.Fatal("short request")
	}
	return binary.NativeEndian.Uint32(b), uint16(binary.NativeEndian.Uint32(b[4:])), b[8:n]
}
func noerr(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func bind(t *testing.T, d *wlturbo.Display, p *net.UnixConn, name uint32, iface string, announced uint32, supported uint32, proxy wlturbo.Proxy) {
	t.Helper()
	// A fake server must advertise a global before clients can negotiate it.
	announcement := append(msg(0, 0, name)[8:], str(iface)...)
	announcement = append(announcement, msg(0, 0, announced)[8:]...)
	send(t, p, payload(d.Registry().ID(), 0, announcement...))
	noerr(t, d.Dispatch())
	v, err := d.Registry().BindNegotiated(iface, supported, proxy)
	noerr(t, err)
	want := min(announced, supported)
	_, op, b := request(t, p)
	if op != 0 || v != want || binary.NativeEndian.Uint32(b) != name || binary.NativeEndian.Uint32(b[len(b)-8:]) != want || binary.NativeEndian.Uint32(b[len(b)-4:]) != proxy.ID() {
		t.Fatalf("bad bind %s announced=%d supported=%d negotiated=%d wire=%x", iface, announced, supported, v, b)
	}
}
func TestExtensionsOverSocketpair(t *testing.T) {
	c, p := pair(t)
	d, e := wlturbo.ConnectFromConn(c)
	noerr(t, e)
	defer d.Close()
	request(t, p)
	// A registry announcement is the only input to optional-global discovery.
	send(t, p, payload(d.Registry().ID(), 0, append(msg(0, 0, 11)[8:12], append(str(fractionalscale.WpFractionalScaleManagerInterface), msg(0, 0, 1)[8:12]...)...)...))
	noerr(t, d.Dispatch())
	if g, ok := d.Registry().FindGlobal(fractionalscale.WpFractionalScaleManagerInterface); !ok || g.Version != 1 {
		t.Fatal("announced fractional scale missing", g, ok)
	}
	if _, ok := d.Registry().FindGlobal(textinput.TextInputManagerV3Interface); ok {
		t.Fatal("absent text input")
	}
	// Remove the discovery-only announcement before the bind matrix assigns
	// its own global name to this interface.
	send(t, p, msg(d.Registry().ID(), 1, 11))
	noerr(t, d.Dispatch())
	surface := core.NewSurface(d.Context())
	surface.SetID(d.AllocateID())
	d.Context().Register(surface)
	seat := core.NewSeat(d.Context())
	seat.SetID(d.AllocateID())
	d.Context().Register(seat)
	wm := xdgshell.NewXdgWmBase(d.Context())
	bind(t, d, p, 1, xdgshell.XdgWmBaseInterface, 6, 7, wm)
	xs, e := wm.GetXdgSurface(surface)
	noerr(t, e)
	request(t, p)
	top, e := xs.GetToplevel()
	noerr(t, e)
	request(t, p)
	var sequence []string
	top.OnConfigure(func(w, h int32, states []byte) {
		if w != 800 || h != 600 {
			t.Fatalf("size %d,%d", w, h)
		}
		sequence = append(sequence, "top")
	})
	xs.OnConfigure(func(serial uint32) { sequence = append(sequence, "surface"); noerr(t, xs.AckConfigure(serial)) })
	send(t, p, payload(top.ID(), 0, append(msg(0, 0, 800, 600)[8:], arr(nil)...)...))
	noerr(t, d.Dispatch())
	send(t, p, msg(xs.ID(), 0, 9))
	noerr(t, d.Dispatch())
	_, op, b := request(t, p)
	if op != 4 || binary.NativeEndian.Uint32(b) != 9 || strings.Join(sequence, ",") != "top,surface" {
		t.Fatalf("xdg %d %x %v", op, b, sequence)
	}
	noerr(t, top.Destroy())
	request(t, p)
	noerr(t, xs.Destroy())
	request(t, p)
	noerr(t, wm.Destroy())
	request(t, p)
	dm := linuxdmabuf.NewLinuxDmabuf(d.Context())
	bind(t, d, p, 2, linuxdmabuf.LinuxDmabufInterface, 6, 4, dm)
	fb, e := dm.GetDefaultFeedback()
	noerr(t, e)
	request(t, p)
	table, e := os.CreateTemp(t.TempDir(), "table")
	noerr(t, e)
	var bytes [32]byte
	binary.NativeEndian.PutUint32(bytes[:], 0x34325258)
	binary.NativeEndian.PutUint64(bytes[8:], 19)
	binary.NativeEndian.PutUint32(bytes[16:], 0x34325241)
	binary.NativeEndian.PutUint64(bytes[24:], 42)
	_, e = table.Write(bytes[:])
	noerr(t, e)
	defer table.Close()
	var entries []linuxdmabuf.FormatEntry
	var indices []uint16
	var feedback []string
	fb.OnMainDevice(func(dev []byte) {
		if len(dev) != 8 {
			t.Fatalf("device %x", dev)
		}
		feedback = append(feedback, "main")
	})
	fb.OnFormatTable(func(fd *wlturbo.OwnedFD, size uint32) {
		entries, e = linuxdmabuf.ReadFormatTable(fd, size)
		noerr(t, e)
		feedback = append(feedback, "table")
	})
	fb.OnTrancheTargetDevice(func(dev []byte) { feedback = append(feedback, "target") })
	fb.OnTrancheFormats(func(data []byte) {
		indices, e = linuxdmabuf.TrancheIndices(data, len(entries))
		noerr(t, e)
		feedback = append(feedback, "formats")
	})
	fb.OnTrancheFlags(func(flags uint32) {
		if flags != 1 {
			t.Fatal(flags)
		}
		feedback = append(feedback, "flags")
	})
	fb.OnTrancheDone(func() { feedback = append(feedback, "tranche") })
	fb.OnDone(func() { feedback = append(feedback, "done") })
	send(t, p, payload(fb.ID(), 2, arr(make([]byte, 8))...))
	noerr(t, d.Dispatch())
	send(t, p, msg(fb.ID(), 1, 32), int(table.Fd()))
	noerr(t, d.Dispatch())
	send(t, p, payload(fb.ID(), 4, arr(make([]byte, 8))...))
	noerr(t, d.Dispatch())
	send(t, p, payload(fb.ID(), 5, arr([]byte{0, 0, 1, 0})...))
	noerr(t, d.Dispatch())
	send(t, p, msg(fb.ID(), 6, 1))
	noerr(t, d.Dispatch())
	send(t, p, msg(fb.ID(), 3))
	noerr(t, d.Dispatch())
	send(t, p, msg(fb.ID(), 0))
	noerr(t, d.Dispatch())
	if len(entries) != 2 || entries[1].Modifier != 42 || len(indices) != 2 || indices[1] != 1 || strings.Join(feedback, ",") != "main,table,target,formats,flags,tranche,done" {
		t.Fatalf("feedback %v %v %v", entries, indices, feedback)
	}
	noerr(t, fb.Destroy())
	request(t, p)
	params, e := dm.CreateParams()
	noerr(t, e)
	request(t, p)
	f, e := os.Open("/dev/null")
	noerr(t, e)
	noerr(t, params.Add(int(f.Fd()), 0, 0, 4, 0, 0))
	request(t, p)
	if _, e := unix.FcntlInt(f.Fd(), unix.F_GETFD, 0); e == nil {
		t.Fatal("sent fd not closed")
	}
	f.Close()
	var buffer *core.Buffer
	params.OnCreated(func(b *core.Buffer) { buffer = b })
	params.OnFailed(func() { sequence = append(sequence, "failed") })
	noerr(t, params.Create(1, 1, 0x34325258, 0))
	request(t, p)
	send(t, p, msg(params.ID(), 0, 999))
	noerr(t, d.Dispatch())
	if buffer == nil || buffer.ID() != 999 {
		t.Fatal("typed buffer missing")
	}
	noerr(t, buffer.Destroy())
	request(t, p)
	noerr(t, params.Destroy())
	request(t, p)
	failedParams, e := dm.CreateParams()
	noerr(t, e)
	request(t, p)
	failedParams.OnFailed(func() { sequence = append(sequence, "failed") })
	noerr(t, failedParams.Create(1, 1, 0x34325258, 0))
	request(t, p)
	send(t, p, msg(failedParams.ID(), 1))
	noerr(t, d.Dispatch())
	if sequence[len(sequence)-1] != "failed" {
		t.Fatal(sequence)
	}
	noerr(t, failedParams.Destroy())
	request(t, p)
	immedParams, e := dm.CreateParams()
	noerr(t, e)
	request(t, p)
	immediate, e := immedParams.CreateImmed(1, 1, 0x34325258, 0)
	noerr(t, e)
	request(t, p)
	noerr(t, immediate.Destroy())
	request(t, p)
	noerr(t, immedParams.Destroy())
	request(t, p)
	noerr(t, dm.Destroy())
	request(t, p)
	sync := drmsyncobj.NewWpLinuxDrmSyncobjManager(d.Context())
	bind(t, d, p, 3, drmsyncobj.WpLinuxDrmSyncobjManagerInterface, 1, 1, sync)
	ss, e := sync.GetSurface(surface)
	noerr(t, e)
	request(t, p)
	f, e = os.Open("/dev/null")
	noerr(t, e)
	tl, e := sync.ImportTimeline(int(f.Fd()))
	noerr(t, e)
	request(t, p)
	if _, e := unix.FcntlInt(f.Fd(), unix.F_GETFD, 0); e == nil {
		t.Fatal("timeline fd not closed")
	}
	f.Close()
	noerr(t, ss.SetAcquirePoint(tl, 0x123, 0x456))
	_, op, b = request(t, p)
	if op != 1 || binary.NativeEndian.Uint32(b[4:]) != 0x123 || binary.NativeEndian.Uint32(b[8:]) != 0x456 {
		t.Fatalf("acquire %d %x", op, b)
	}
	noerr(t, ss.SetReleasePoint(tl, 0x789, 0xabc))
	_, op, b = request(t, p)
	if op != 2 || binary.NativeEndian.Uint32(b[4:]) != 0x789 {
		t.Fatalf("release %d %x", op, b)
	}
	noerr(t, ss.Destroy())
	request(t, p)
	noerr(t, tl.Destroy())
	request(t, p)
	noerr(t, sync.Destroy())
	request(t, p)
	vp := viewporter.NewWpViewporter(d.Context())
	bind(t, d, p, 4, viewporter.WpViewporterInterface, 1, 1, vp)
	view, e := vp.GetViewport(surface)
	noerr(t, e)
	request(t, p)
	noerr(t, view.Destroy())
	request(t, p)
	noerr(t, vp.Destroy())
	request(t, p)
	fs := fractionalscale.NewWpFractionalScaleManager(d.Context())
	bind(t, d, p, 5, fractionalscale.WpFractionalScaleManagerInterface, 1, 1, fs)
	scale, e := fs.GetFractionalScale(surface)
	noerr(t, e)
	request(t, p)
	var preferred uint32
	scale.OnPreferredScale(func(v uint32) { preferred = v })
	send(t, p, msg(scale.ID(), 0, 150))
	noerr(t, d.Dispatch())
	if preferred != 150 {
		t.Fatal(preferred)
	}
	noerr(t, scale.Destroy())
	request(t, p)
	noerr(t, fs.Destroy())
	request(t, p)
	ti := textinput.NewTextInputManagerV3(d.Context())
	bind(t, d, p, 6, textinput.TextInputManagerV3Interface, 2, 1, ti)
	input, e := ti.GetTextInput(seat)
	noerr(t, e)
	request(t, p)
	noerr(t, input.Enable())
	request(t, p)
	noerr(t, input.SetSurroundingText("hello", 5, 5))
	request(t, p)
	noerr(t, input.Commit())
	request(t, p)
	var events []string
	input.OnPreeditString(func(s string, _, _ int32) { events = append(events, "preedit:"+s) })
	input.OnCommitString(func(s string) { events = append(events, "commit:"+s) })
	input.OnDeleteSurroundingText(func(_, _ uint32) { events = append(events, "delete") })
	input.OnDone(func(uint32) { events = append(events, "done") })
	send(t, p, payload(input.ID(), 2, append(str("é"), msg(0, 0, 0, 0)[8:]...)...))
	noerr(t, d.Dispatch())
	send(t, p, payload(input.ID(), 3, str("hi")...))
	noerr(t, d.Dispatch())
	send(t, p, msg(input.ID(), 4, 1, 2))
	noerr(t, d.Dispatch())
	send(t, p, msg(input.ID(), 5, 1))
	noerr(t, d.Dispatch())
	if strings.Join(events, ",") != "preedit:é,commit:hi,delete,done" {
		t.Fatal(events)
	}
	noerr(t, input.Disable())
	request(t, p)
	noerr(t, input.Commit())
	request(t, p)
	noerr(t, input.Destroy())
	request(t, p)
	noerr(t, ti.Destroy())
	request(t, p)
	// Core data-device remains in protocol/core; verify its typed child and
	// both directions of clipboard descriptor ownership.
	mgr := core.NewDataDeviceManager(d.Context())
	bind(t, d, p, 7, core.DataDeviceManagerInterface, 3, 4, mgr)
	device, e := mgr.GetDataDevice(seat)
	noerr(t, e)
	request(t, p)
	source, e := mgr.CreateDataSource()
	noerr(t, e)
	request(t, p)
	var offer *core.DataOffer
	device.OnDataOffer(func(o *core.DataOffer) { offer = o })
	send(t, p, msg(device.ID(), 0, 998))
	noerr(t, d.Dispatch())
	if offer == nil || offer.ID() != 998 {
		t.Fatal("offer not typed")
	}
	read, write, e := os.Pipe()
	noerr(t, e)
	defer read.Close()
	noerr(t, offer.Receive("text/plain", int(write.Fd())))
	request(t, p)
	if _, e := unix.FcntlInt(write.Fd(), unix.F_GETFD, 0); e == nil {
		t.Fatal("clipboard sent fd retained")
	}
	write.Close()
	source.OnSend(func(mime string, fd *wlturbo.OwnedFD) {
		if mime != "text/plain" {
			t.Fatal(mime)
		}
		n, e := fd.Take()
		noerr(t, e)
		unix.Close(n)
	})
	received, e := os.Open("/dev/null")
	noerr(t, e)
	send(t, p, payload(source.ID(), 1, str("text/plain")...), int(received.Fd()))
	noerr(t, d.Dispatch())
	received.Close()
	noerr(t, offer.Destroy())
	request(t, p)
	noerr(t, source.Destroy())
	request(t, p)
	noerr(t, device.Release())
	request(t, p)
	// release is a version 4 request; the manager was negotiated at 3, so it
	// must be refused locally instead of reaching the compositor.
	if err := mgr.Release(); !errors.Is(err, wlturbo.ErrVersionTooLow) {
		t.Fatalf("release at v3 = %v, want ErrVersionTooLow", err)
	}
	// Cursor shape: get a device for a wl_pointer and set a shape at an
	// enter serial; check the exact wire arguments.
	pointer := core.NewPointer(d.Context())
	pointer.SetID(d.AllocateID())
	d.Context().Register(pointer)
	shapes := cursorshape.NewWpCursorShapeManager(d.Context())
	bind(t, d, p, 8, cursorshape.WpCursorShapeManagerInterface, 2, 1, shapes)
	shape, e := shapes.GetPointer(pointer)
	noerr(t, e)
	if id, op, b := request(t, p); id != shapes.ID() || op != 1 || binary.NativeEndian.Uint32(b) != shape.ID() || binary.NativeEndian.Uint32(b[4:]) != pointer.ID() {
		t.Fatalf("get_pointer wire id=%d op=%d %x", id, op, b)
	}
	noerr(t, shape.SetShape(42, cursorshape.SHAPE_POINTER))
	if id, op, b := request(t, p); id != shape.ID() || op != 1 || binary.NativeEndian.Uint32(b) != 42 || binary.NativeEndian.Uint32(b[4:]) != 4 {
		t.Fatalf("set_shape wire id=%d op=%d %x", id, op, b)
	}
	noerr(t, shape.Destroy())
	request(t, p)
	noerr(t, shapes.Destroy())
	request(t, p)

}

func TestLiveFeedback(t *testing.T) {
	if os.Getenv("WLTURBO_LIVE") != "1" {
		t.Skip("set WLTURBO_LIVE=1")
	}
	d, e := wlturbo.Connect("")
	noerr(t, e)
	defer d.Close()
	noerr(t, d.Roundtrip())
	for _, iface := range []string{xdgshell.XdgWmBaseInterface, linuxdmabuf.LinuxDmabufInterface, drmsyncobj.WpLinuxDrmSyncobjManagerInterface, viewporter.WpViewporterInterface, fractionalscale.WpFractionalScaleManagerInterface, textinput.TextInputManagerV3Interface, cursorshape.WpCursorShapeManagerInterface} {
		g, ok := d.Registry().FindGlobal(iface)
		if !ok {
			t.Logf("%s absent", iface)
			continue
		}
		v := g.Version
		cap := uint32(1)
		if iface == xdgshell.XdgWmBaseInterface {
			cap = 7
		}
		if iface == linuxdmabuf.LinuxDmabufInterface {
			cap = 4
		}
		if v > cap {
			v = cap
		}
		t.Logf("%s negotiated=%d (server=%d)", iface, v, g.Version)
		if iface != linuxdmabuf.LinuxDmabufInterface {
			continue
		}
		if v < 4 {
			t.Fatal("dmabuf feedback requires v4")
		}
		dm := linuxdmabuf.NewLinuxDmabuf(d.Context())
		negotiated, err := d.Registry().BindNegotiated(iface, 4, dm)
		noerr(t, err)
		if negotiated != v {
			t.Fatalf("dmabuf negotiated=%d want=%d", negotiated, v)
		}
		fb, e := dm.GetDefaultFeedback()
		noerr(t, e)
		done := false
		fb.OnMainDevice(func(b []byte) { t.Logf("main_device=%x", b) })
		fb.OnFormatTable(func(fd *wlturbo.OwnedFD, size uint32) {
			entries, err := linuxdmabuf.ReadFormatTable(fd, size)
			if err != nil {
				t.Errorf("table: %v", err)
			}
			t.Logf("format-table entries=%d", len(entries))
		})
		fb.OnDone(func() { done = true })
		for i := 0; i < 100 && !done; i++ {
			noerr(t, d.Dispatch())
		}
		if !done {
			t.Fatal("no feedback done")
		}
		noerr(t, fb.Destroy())
		noerr(t, dm.Destroy())
	}
}
