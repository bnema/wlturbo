//go:build linux

package protocol_test

// Group A client bindings (presentation timing, surface render hints,
// activation, pointer warp, colour and DRM lease). Every test drives a real
// Display over a socketpair and checks the bytes the client writes and the
// bytes it accepts, not just that a method exists.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"testing"

	"github.com/bnema/wlturbo"
	"github.com/bnema/wlturbo/protocol/alphamodifier"
	"github.com/bnema/wlturbo/protocol/colormanagement"
	"github.com/bnema/wlturbo/protocol/colorrepresentation"
	"github.com/bnema/wlturbo/protocol/committiming"
	"github.com/bnema/wlturbo/protocol/contenttype"
	"github.com/bnema/wlturbo/protocol/core"
	"github.com/bnema/wlturbo/protocol/drmlease"
	"github.com/bnema/wlturbo/protocol/fifo"
	"github.com/bnema/wlturbo/protocol/pointerwarp"
	"github.com/bnema/wlturbo/protocol/presentation"
	"github.com/bnema/wlturbo/protocol/tearingcontrol"
	"github.com/bnema/wlturbo/protocol/xdgactivation"
	"golang.org/x/sys/unix"
)

// rxEnv is the shared wire environment plus one object of each core kind the
// group A protocols refer to.
type rxEnv struct {
	*wireEnv
	surface *core.Surface
	output  *core.Output
	seat    *core.Seat
	pointer *core.Pointer
}

func rxNew(t *testing.T) *rxEnv {
	t.Helper()
	s := newWireEnv(t)
	return &rxEnv{wireEnv: s, surface: s.surface(), output: s.output(), seat: s.seat(), pointer: s.pointer()}
}

// rxWant reads one request and requires its exact object, opcode and
// 32-bit word arguments.
func rxWant(t *testing.T, p *net.UnixConn, id uint32, op uint16, want ...uint32) {
	t.Helper()
	wantRequestWords(t, p, id, op, want...)
}

// rxWantRaw reads one request and requires its exact object, opcode and body.
func rxWantRaw(t *testing.T, p *net.UnixConn, id uint32, op uint16, want []byte) {
	t.Helper()
	gid, gop, body := request(t, p)
	if gid != id || gop != op || !bytes.Equal(body, want) {
		t.Fatalf("request object=%d op=%d body=%x, want object=%d op=%d body=%x", gid, gop, body, id, op, want)
	}
}

// rxRebind binds a second object to an interface that is already bound: the
// earlier announcement is withdrawn first so BindNegotiated finds the new one.
func rxRebind(e *rxEnv, oldName, name uint32, iface string, announced, supported uint32, proxy wlturbo.Proxy) {
	e.t.Helper()
	e.event(e.d.Registry().ID(), 1, wordBytes(oldName)...)
	e.bind(name, iface, announced, supported, proxy)
}

func rxOpenFDs(t *testing.T) int {
	t.Helper()
	ents, err := os.ReadDir("/proc/self/fd")
	noerr(t, err)
	return len(ents)
}

func rxTempFile(t *testing.T, content string) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "rx")
	noerr(t, err)
	_, err = f.WriteString(content)
	noerr(t, err)
	t.Cleanup(func() { f.Close() })
	return f
}

// rxReadFD reads want bytes at offset 0 of a descriptor and closes it.
func rxReadFD(t *testing.T, fd int, want string) {
	t.Helper()
	buf := make([]byte, len(want))
	n, err := unix.Pread(fd, buf, 0)
	noerr(t, err)
	if string(buf[:n]) != want {
		t.Fatalf("fd content %q, want %q", buf[:n], want)
	}
	noerr(t, unix.Close(fd))
}

func TestPresentationFeedbackDestructorEvents(t *testing.T) {
	e := rxNew(t)
	pres := presentation.NewWpPresentation(e.d.Context())
	bind(t, e.d, e.p, 1, presentation.WpPresentationInterface, 2, 2, pres)
	if pres.Version() != 2 {
		t.Fatalf("version %d", pres.Version())
	}
	var clock uint32
	pres.OnClockId(func(id uint32) { clock = id })
	e.eventFD(pres.ID(), 0, wordBytes(1)) // CLOCK_MONOTONIC
	if clock != 1 {
		t.Fatalf("clock %d", clock)
	}

	fb1, err := pres.Feedback(e.surface)
	noerr(t, err)
	rxWant(t, e.p, pres.ID(), 1, e.surface.ID(), fb1.ID())
	fb2, err := pres.Feedback(e.surface)
	noerr(t, err)
	rxWant(t, e.p, pres.ID(), 1, e.surface.ID(), fb2.ID())
	if fb1.Version() != 2 || fb1.ID() == fb2.ID() {
		t.Fatalf("children id=%d/%d version=%d", fb1.ID(), fb2.ID(), fb1.Version())
	}

	// Destroying the manager must not affect feedback it already created.
	noerr(t, pres.Destroy())
	rxWant(t, e.p, pres.ID(), 0)
	if err := pres.Destroy(); err == nil {
		t.Fatal("second manager destroy accepted")
	}

	type presented struct{ hi, lo, nsec, refresh, seqHi, seqLo, flags uint32 }
	var outputs []uint32
	var got []presented
	fb1.OnSyncOutput(func(id uint32) { outputs = append(outputs, id) })
	fb1.OnPresented(func(hi, lo, nsec, refresh, sh, sl, flags uint32) {
		got = append(got, presented{hi, lo, nsec, refresh, sh, sl, flags})
	})
	discarded := 0
	fb2.OnDiscarded(func() { discarded++ })
	fb2.OnPresented(func(uint32, uint32, uint32, uint32, uint32, uint32, uint32) { t.Fatal("discarded feedback presented") })

	e.eventFD(fb1.ID(), 0, wordBytes(e.output.ID()))
	if len(outputs) != 1 || outputs[0] != e.output.ID() {
		t.Fatalf("sync_output %v", outputs)
	}
	// A presented event missing its last argument is malformed and must not
	// destroy the object or reach the handler.
	send(t, e.p, payload(fb1.ID(), 1, wordBytes(0, 1, 2, 3, 4, 5)...))
	if err := e.d.Dispatch(); !errors.Is(err, wlturbo.ErrMalformedFrame) {
		t.Fatalf("short presented = %v, want ErrMalformedFrame", err)
	}
	if len(got) != 0 {
		t.Fatal("handler ran for a malformed event")
	}
	flags := uint32(presentation.KIND_VSYNC | presentation.KIND_HW_CLOCK | presentation.KIND_ZERO_COPY)
	e.eventFD(fb1.ID(), 1, wordBytes(0, 0x12345678, 999999999, 16666666, 1, 2, flags))
	want := presented{0, 0x12345678, 999999999, 16666666, 1, 2, 0xb}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("presented %+v, want %+v", got, want)
	}

	e.eventFD(fb2.ID(), 2, nil)
	if discarded != 1 {
		t.Fatalf("discarded %d", discarded)
	}

	// Both terminal events are destructors: the objects are gone, so a
	// further event for either is an unknown-object protocol error.
	send(t, e.p, msg(fb1.ID(), 2))
	if err := e.d.Dispatch(); !errors.Is(err, wlturbo.ErrUnknownObject) {
		t.Fatalf("event after presented = %v, want ErrUnknownObject", err)
	}
	send(t, e.p, msg(fb2.ID(), 2))
	if err := e.d.Dispatch(); !errors.Is(err, wlturbo.ErrUnknownObject) {
		t.Fatalf("event after discarded = %v, want ErrUnknownObject", err)
	}
}

func TestPresentationVersionNegotiation(t *testing.T) {
	e := rxNew(t)
	old := presentation.NewWpPresentation(e.d.Context())
	bind(t, e.d, e.p, 1, presentation.WpPresentationInterface, 1, 2, old)
	if old.Version() != 1 {
		t.Fatalf("server v1: bound at %d", old.Version())
	}
	fb, err := old.Feedback(e.surface)
	noerr(t, err)
	if fb.Version() != 1 {
		t.Fatalf("child version %d, want the parent's 1", fb.Version())
	}
	rxWant(t, e.p, old.ID(), 1, e.surface.ID(), fb.ID())

	newer := presentation.NewWpPresentation(e.d.Context())
	rxRebind(e, 1, 2, presentation.WpPresentationInterface, 5, 2, newer)
	if newer.Version() != 2 {
		t.Fatalf("server v5: bound at %d, want the supported 2", newer.Version())
	}
	absent := presentation.NewWpPresentation(e.d.Context())
	if _, err := e.d.Registry().BindNegotiated(presentation.WpPresentationInterface+"_x", 2, absent); !errors.Is(err, wlturbo.ErrGlobalNotFound) {
		t.Fatalf("absent global = %v", err)
	}
	requireNoWrite(t, e.p)
}

func TestColorManagementVersionRejection(t *testing.T) {
	e := rxNew(t)
	cm := colormanagement.NewWpColorManager(e.d.Context())
	bind(t, e.d, e.p, 1, colormanagement.WpColorManagerInterface, 1, 3, cm)
	if cm.Version() != 1 {
		t.Fatalf("bound at %d", cm.Version())
	}
	// Version 1 requests still work...
	surf, err := cm.GetSurface(e.surface)
	noerr(t, err)
	rxWant(t, e.p, cm.ID(), 2, surf.ID(), e.surface.ID())
	// ...but the version 2 and 3 additions are refused locally, without a
	// byte reaching the compositor and without consuming an object ID.
	ref := colormanagement.NewWpImageDescriptionReference(e.d.Context())
	ref.SetID(e.d.AllocateID())
	e.d.Context().Register(ref)
	next := e.d.AllocateID()
	if _, err := cm.GetImageDescription(ref); !errors.Is(err, wlturbo.ErrVersionTooLow) {
		t.Fatalf("get_image_description on v1 = %v", err)
	}
	if _, err := cm.CreateWindowsBt2100(); !errors.Is(err, wlturbo.ErrVersionTooLow) {
		t.Fatalf("create_windows_bt2100 on v1 = %v", err)
	}
	requireNoWrite(t, e.p)
	if after := e.d.AllocateID(); after != next+1 {
		t.Fatalf("rejected requests allocated ids: %d then %d", next, after)
	}

	// Announced v2 admits get_image_description but still not the v3 request.
	cm2 := colormanagement.NewWpColorManager(e.d.Context())
	rxRebind(e, 1, 2, colormanagement.WpColorManagerInterface, 2, 3, cm2)
	desc, err := cm2.GetImageDescription(ref)
	noerr(t, err)
	rxWant(t, e.p, cm2.ID(), 7, desc.ID(), ref.ID())
	if _, err := cm2.CreateWindowsBt2100(); !errors.Is(err, wlturbo.ErrVersionTooLow) {
		t.Fatalf("create_windows_bt2100 on v2 = %v", err)
	}
	requireNoWrite(t, e.p)

	cm3 := colormanagement.NewWpColorManager(e.d.Context())
	rxRebind(e, 2, 3, colormanagement.WpColorManagerInterface, 3, 3, cm3)
	bt, err := cm3.CreateWindowsBt2100()
	noerr(t, err)
	rxWant(t, e.p, cm3.ID(), 8, bt.ID())
}

func TestColorManagementParametricWire(t *testing.T) {
	e := rxNew(t)
	cm := colormanagement.NewWpColorManager(e.d.Context())
	bind(t, e.d, e.p, 1, colormanagement.WpColorManagerInterface, 3, 3, cm)

	var seq []string
	cm.OnSupportedIntent(func(v uint32) {
		if v != colormanagement.RENDER_INTENT_PERCEPTUAL {
			t.Fatal(v)
		}
		seq = append(seq, "intent")
	})
	cm.OnSupportedFeature(func(v uint32) {
		if v != colormanagement.FEATURE_PARAMETRIC {
			t.Fatal(v)
		}
		seq = append(seq, "feature")
	})
	cm.OnSupportedTfNamed(func(v uint32) {
		if v != colormanagement.TRANSFER_FUNCTION_ST2084_PQ {
			t.Fatal(v)
		}
		seq = append(seq, "tf")
	})
	cm.OnSupportedPrimariesNamed(func(v uint32) {
		if v != colormanagement.PRIMARIES_SRGB {
			t.Fatal(v)
		}
		seq = append(seq, "primaries")
	})
	cm.OnDone(func() { seq = append(seq, "done") })
	e.eventFD(cm.ID(), 0, wordBytes(colormanagement.RENDER_INTENT_PERCEPTUAL))
	e.eventFD(cm.ID(), 1, wordBytes(colormanagement.FEATURE_PARAMETRIC))
	e.eventFD(cm.ID(), 2, wordBytes(colormanagement.TRANSFER_FUNCTION_ST2084_PQ))
	e.eventFD(cm.ID(), 3, wordBytes(colormanagement.PRIMARIES_SRGB))
	e.eventFD(cm.ID(), 4, nil)
	if len(seq) != 5 || seq[0] != "intent" || seq[4] != "done" {
		t.Fatalf("manager events %v", seq)
	}

	out, err := cm.GetOutput(e.output)
	noerr(t, err)
	rxWant(t, e.p, cm.ID(), 1, out.ID(), e.output.ID())
	changed := 0
	out.OnImageDescriptionChanged(func() { changed++ })
	e.eventFD(out.ID(), 0, nil)
	if changed != 1 {
		t.Fatal(changed)
	}
	outDesc, err := out.GetImageDescription()
	noerr(t, err)
	rxWant(t, e.p, out.ID(), 1, outDesc.ID())

	creator, err := cm.CreateParametricCreator()
	noerr(t, err)
	rxWant(t, e.p, cm.ID(), 5, creator.ID())
	noerr(t, creator.SetTfNamed(colormanagement.TRANSFER_FUNCTION_ST2084_PQ))
	rxWant(t, e.p, creator.ID(), 1, colormanagement.TRANSFER_FUNCTION_ST2084_PQ)
	noerr(t, creator.SetTfPower(22000))
	rxWant(t, e.p, creator.ID(), 2, 22000)
	noerr(t, creator.SetPrimariesNamed(colormanagement.PRIMARIES_SRGB))
	rxWant(t, e.p, creator.ID(), 3, colormanagement.PRIMARIES_SRGB)
	// Chromaticities are signed: negative values must survive as two's
	// complement words.
	noerr(t, creator.SetPrimaries(640000, 330000, 300000, 600000, 150000, 60000, -312700, 329000))
	negWhiteX := int32(-312700)
	rxWant(t, e.p, creator.ID(), 4, 640000, 330000, 300000, 600000, 150000, 60000, uint32(negWhiteX), 329000)
	noerr(t, creator.SetLuminances(2, 10000, 203))
	rxWant(t, e.p, creator.ID(), 5, 2, 10000, 203)
	noerr(t, creator.SetMasteringDisplayPrimaries(1, -2, 3, -4, 5, -6, 7, -8))
	rxWant(t, e.p, creator.ID(), 6, 1, 0xfffffffe, 3, 0xfffffffc, 5, 0xfffffffa, 7, 0xfffffff8)
	noerr(t, creator.SetMasteringLuminance(50, 1000))
	rxWant(t, e.p, creator.ID(), 7, 50, 1000)
	noerr(t, creator.SetMaxCll(1000))
	rxWant(t, e.p, creator.ID(), 8, 1000)
	noerr(t, creator.SetMaxFall(400))
	rxWant(t, e.p, creator.ID(), 9, 400)

	desc, err := creator.Create()
	noerr(t, err)
	rxWant(t, e.p, creator.ID(), 0, desc.ID())
	// create is a destructor: the creator cannot be used again.
	if err := creator.SetMaxCll(1); err == nil {
		t.Fatal("request on a consumed creator accepted")
	}
	requireNoWrite(t, e.p)

	var ready [][2]uint32
	desc.OnReady2(func(hi, lo uint32) { ready = append(ready, [2]uint32{hi, lo}) })
	e.eventFD(desc.ID(), 2, wordBytes(0xdeadbeef, 7))
	if len(ready) != 1 || ready[0] != [2]uint32{0xdeadbeef, 7} {
		t.Fatalf("ready2 %v", ready)
	}

	cs, err := cm.GetSurface(e.surface)
	noerr(t, err)
	rxWant(t, e.p, cm.ID(), 2, cs.ID(), e.surface.ID())
	noerr(t, cs.SetImageDescription(desc, colormanagement.RENDER_INTENT_PERCEPTUAL))
	rxWant(t, e.p, cs.ID(), 1, desc.ID(), colormanagement.RENDER_INTENT_PERCEPTUAL)
	noerr(t, cs.UnsetImageDescription())
	rxWant(t, e.p, cs.ID(), 2)
	// A nil description travels as object 0.
	noerr(t, cs.SetImageDescription(nil, 0))
	rxWant(t, e.p, cs.ID(), 1, 0, 0)

	// A refused creation arrives as failed(cause, msg).
	scrgb, err := cm.CreateWindowsScrgb()
	noerr(t, err)
	rxWant(t, e.p, cm.ID(), 6, scrgb.ID())
	var cause uint32
	var why string
	scrgb.OnFailed(func(c uint32, m string) { cause, why = c, m })
	e.eventFD(scrgb.ID(), 0, cat(wordBytes(colormanagement.CAUSE_UNSUPPORTED), str("not supported")))
	if cause != colormanagement.CAUSE_UNSUPPORTED || why != "not supported" {
		t.Fatalf("failed %d %q", cause, why)
	}

	fbk, err := cm.GetSurfaceFeedback(e.surface)
	noerr(t, err)
	rxWant(t, e.p, cm.ID(), 3, fbk.ID(), e.surface.ID())
	var pref [2]uint32
	fbk.OnPreferredChanged2(func(hi, lo uint32) { pref = [2]uint32{hi, lo} })
	e.eventFD(fbk.ID(), 1, wordBytes(1, 2))
	if pref != [2]uint32{1, 2} {
		t.Fatalf("preferred_changed2 %v", pref)
	}
	pd, err := fbk.GetPreferredParametric()
	noerr(t, err)
	rxWant(t, e.p, fbk.ID(), 2, pd.ID())

	noerr(t, desc.Destroy())
	rxWant(t, e.p, desc.ID(), 0)
	noerr(t, cs.Destroy())
	rxWant(t, e.p, cs.ID(), 0)
	noerr(t, cm.Destroy())
	rxWant(t, e.p, cm.ID(), 0)
}

func TestColorManagementICCDescriptorOwnership(t *testing.T) {
	e := rxNew(t)
	cm := colormanagement.NewWpColorManager(e.d.Context())
	bind(t, e.d, e.p, 1, colormanagement.WpColorManagerInterface, 3, 3, cm)

	icc, err := cm.CreateIccCreator()
	noerr(t, err)
	rxWant(t, e.p, cm.ID(), 4, icc.ID())

	// Request direction: the descriptor is attached as SCM_RIGHTS, the body
	// carries only offset and length, and the client closes its copy.
	profile := rxTempFile(t, "ICC-PROFILE-BYTES")
	fd, err := unix.Dup(int(profile.Fd()))
	noerr(t, err)
	noerr(t, icc.SetIccFile(fd, 4, 13))
	requireFDClosed(t, fd)
	id, op, body, fds := recvRequestFDs(t, e.p)
	if id != icc.ID() || op != 1 || len(body) != 8 ||
		binary.NativeEndian.Uint32(body) != 4 || binary.NativeEndian.Uint32(body[4:]) != 13 || len(fds) != 1 {
		t.Fatalf("set_icc_file id=%d op=%d body=%x fds=%v", id, op, body, fds)
	}
	rxReadFD(t, fds[0], "ICC-PROFILE-BYTES")

	// A failed send must leave the descriptor with the caller.
	fd2, err := unix.Dup(int(profile.Fd()))
	noerr(t, err)
	created, err := icc.Create()
	noerr(t, err)
	rxWant(t, e.p, icc.ID(), 0, created.ID())
	if err := icc.SetIccFile(fd2, 0, 1); err == nil {
		t.Fatal("set_icc_file on a consumed creator accepted")
	}
	if _, err := unix.FcntlInt(uintptr(fd2), unix.F_GETFD, 0); err != nil {
		t.Fatalf("descriptor of a rejected request was closed: %v", err)
	}
	noerr(t, unix.Close(fd2))
	requireNoWrite(t, e.p)

	// Event direction: image_description_info.icc_file carries an owned fd.
	info, err := created.GetInformation()
	noerr(t, err)
	rxWant(t, e.p, created.ID(), 1, info.ID())

	// The first handler is offered the descriptor and must Take it during
	// dispatch; anything left is closed when dispatch returns. Later handlers
	// only ever see nil.
	var first, second *wlturbo.OwnedFD
	var taken int
	var size uint32
	src2 := rxTempFile(t, "ICC-FROM-COMPOSITOR")
	before := rxOpenFDs(t)
	info.OnIccFile(func(f *wlturbo.OwnedFD, n uint32) {
		first, size = f, n
		var err error
		taken, err = f.Take()
		noerr(t, err)
	})
	info.OnIccFile(func(f *wlturbo.OwnedFD, n uint32) { second = f })
	var prim [8]int32
	info.OnPrimaries(func(a, b, c, d, e2, f, g, h int32) { prim = [8]int32{a, b, c, d, e2, f, g, h} })
	doneCalls := 0
	info.OnDone(func() { doneCalls++ })

	e.eventFD(info.ID(), 1, wordBytes(19), int(src2.Fd()))
	if first == nil || second != nil || size != 19 {
		t.Fatalf("icc_file first=%v second=%v size=%d", first, second, size)
	}
	// The taken descriptor survives dispatch and is now the caller's.
	if now := rxOpenFDs(t); now != before+1 {
		t.Fatalf("open fds %d -> %d, want +1", before, now)
	}
	rxReadFD(t, taken, "ICC-FROM-COMPOSITOR")
	if now := rxOpenFDs(t); now != before {
		t.Fatalf("open fds %d after close, want %d", now, before)
	}
	if _, err := first.Take(); err == nil {
		t.Fatal("descriptor taken twice")
	}

	e.eventFD(info.ID(), 2, wordBytes(1, 0xfffffffe, 3, 4, 5, 6, 7, 8))
	if prim != [8]int32{1, -2, 3, 4, 5, 6, 7, 8} {
		t.Fatalf("primaries %v", prim)
	}
	e.eventFD(info.ID(), 0, nil)
	if doneCalls != 1 {
		t.Fatal(doneCalls)
	}
	send(t, e.p, msg(info.ID(), 0))
	if err := e.d.Dispatch(); !errors.Is(err, wlturbo.ErrUnknownObject) {
		t.Fatalf("event after done = %v", err)
	}

	// A descriptor no handler takes is closed when dispatch returns.
	info2, err := created.GetInformation()
	noerr(t, err)
	rxWant(t, e.p, created.ID(), 1, info2.ID())
	info2.OnIccFile(func(f *wlturbo.OwnedFD, n uint32) {})
	before = rxOpenFDs(t)
	e.eventFD(info2.ID(), 1, wordBytes(19), int(src2.Fd()))
	if now := rxOpenFDs(t); now != before {
		t.Fatalf("unclaimed descriptor leaked: %d -> %d", before, now)
	}
	// An icc_file event with no descriptor is a protocol error.
	send(t, e.p, payload(info2.ID(), 1, wordBytes(19)...))
	if err := e.d.Dispatch(); !errors.Is(err, wlturbo.ErrMalformedFrame) {
		t.Fatalf("icc_file without fd = %v", err)
	}
}

func TestDRMLeaseDescriptorsAndLifecycle(t *testing.T) {
	e := rxNew(t)
	dev := drmlease.NewWpDrmLeaseDevice(e.d.Context())
	bind(t, e.d, e.p, 1, drmlease.WpDrmLeaseDeviceInterface, 1, 1, dev)

	var seq []string
	var drm, drm2 *wlturbo.OwnedFD
	var drmFD int
	dev.OnDrmFd(func(f *wlturbo.OwnedFD) {
		drm = f
		var err error
		drmFD, err = f.Take()
		noerr(t, err)
		seq = append(seq, "drm_fd")
	})
	dev.OnDrmFd(func(f *wlturbo.OwnedFD) { drm2 = f })
	var conn *drmlease.WpDrmLeaseConnector
	dev.OnConnector(func(c *drmlease.WpDrmLeaseConnector) { conn = c; seq = append(seq, "connector") })
	dev.OnDone(func() { seq = append(seq, "done") })

	node := rxTempFile(t, "DRM-NODE")
	before := rxOpenFDs(t)
	e.eventFD(dev.ID(), 0, nil, int(node.Fd()))
	if drm == nil || drm2 != nil {
		t.Fatalf("drm_fd owners: first=%v second=%v", drm, drm2)
	}
	if now := rxOpenFDs(t); now != before+1 {
		t.Fatalf("taken drm_fd not live: %d -> %d", before, now)
	}
	rxReadFD(t, drmFD, "DRM-NODE")
	if now := rxOpenFDs(t); now != before {
		t.Fatalf("drm_fd leaked: %d -> %d", before, now)
	}

	// new_id event: the child is created at the server-chosen ID and
	// inherits the device version.
	e.eventFD(dev.ID(), 1, wordBytes(serverID))
	if conn == nil || conn.ID() != serverID || conn.Version() != 1 {
		t.Fatalf("connector %+v", conn)
	}
	var name, desc string
	var connID uint32
	withdrawn := 0
	conn.OnName(func(s string) { name = s })
	conn.OnDescription(func(s string) { desc = s })
	conn.OnConnectorId(func(v uint32) { connID = v })
	conn.OnDone(func() { seq = append(seq, "conn_done") })
	conn.OnWithdrawn(func() { withdrawn++ })
	e.eventFD(conn.ID(), 0, str("HDMI-A-1"))
	e.eventFD(conn.ID(), 1, str("Acme 4K"))
	e.eventFD(conn.ID(), 2, wordBytes(77))
	e.eventFD(conn.ID(), 3, nil)
	e.eventFD(dev.ID(), 2, nil)
	if name != "HDMI-A-1" || desc != "Acme 4K" || connID != 77 || len(seq) != 4 || seq[3] != "done" {
		t.Fatalf("connector %q %q %d seq %v", name, desc, connID, seq)
	}

	req, err := dev.CreateLeaseRequest()
	noerr(t, err)
	rxWant(t, e.p, dev.ID(), 0, req.ID())
	noerr(t, req.RequestConnector(conn))
	rxWant(t, e.p, req.ID(), 0, conn.ID())
	lease, err := req.Submit()
	noerr(t, err)
	rxWant(t, e.p, req.ID(), 1, lease.ID())
	// submit is a destructor: the request is spent.
	if err := req.RequestConnector(conn); err == nil {
		t.Fatal("request after submit accepted")
	}
	if _, err := req.Submit(); err == nil {
		t.Fatal("second submit accepted")
	}
	requireNoWrite(t, e.p)

	var leased *wlturbo.OwnedFD
	var leasedFD int
	finished := 0
	lease.OnLeaseFd(func(f *wlturbo.OwnedFD) {
		leased = f
		var err error
		leasedFD, err = f.Take()
		noerr(t, err)
	})
	lease.OnFinished(func() { finished++ })
	lfile := rxTempFile(t, "LEASED")
	e.eventFD(lease.ID(), 0, nil, int(lfile.Fd()))
	if leased == nil {
		t.Fatal("lease_fd not delivered")
	}
	rxReadFD(t, leasedFD, "LEASED")
	e.eventFD(lease.ID(), 1, nil)
	if finished != 1 {
		t.Fatal(finished)
	}
	noerr(t, lease.Destroy())
	rxWant(t, e.p, lease.ID(), 0)

	// A denied lease sends finished with no descriptor at all.
	req2, err := dev.CreateLeaseRequest()
	noerr(t, err)
	rxWant(t, e.p, dev.ID(), 0, req2.ID())
	noerr(t, req2.RequestConnector(conn))
	rxWant(t, e.p, req2.ID(), 0, conn.ID())
	denied, err := req2.Submit()
	noerr(t, err)
	rxWant(t, e.p, req2.ID(), 1, denied.ID())
	deniedFD := 0
	deniedDone := 0
	denied.OnLeaseFd(func(*wlturbo.OwnedFD) { deniedFD++ })
	denied.OnFinished(func() { deniedDone++ })
	e.eventFD(denied.ID(), 1, nil)
	if deniedFD != 0 || deniedDone != 1 {
		t.Fatalf("denied lease fd=%d finished=%d", deniedFD, deniedDone)
	}

	// An unclaimed lease descriptor is closed by the dispatcher.
	req3, err := dev.CreateLeaseRequest()
	noerr(t, err)
	rxWant(t, e.p, dev.ID(), 0, req3.ID())
	noerr(t, req3.RequestConnector(conn))
	rxWant(t, e.p, req3.ID(), 0, conn.ID())
	l3, err := req3.Submit()
	noerr(t, err)
	rxWant(t, e.p, req3.ID(), 1, l3.ID())
	l3.OnLeaseFd(func(*wlturbo.OwnedFD) {})
	before = rxOpenFDs(t)
	e.eventFD(l3.ID(), 0, nil, int(lfile.Fd()))
	if now := rxOpenFDs(t); now != before {
		t.Fatalf("unclaimed lease fd leaked: %d -> %d", before, now)
	}
	// lease_fd without SCM_RIGHTS is malformed.
	send(t, e.p, msg(l3.ID(), 0))
	if err := e.d.Dispatch(); !errors.Is(err, wlturbo.ErrMalformedFrame) {
		t.Fatalf("lease_fd without fd = %v", err)
	}

	e.eventFD(conn.ID(), 4, nil)
	if withdrawn != 1 {
		t.Fatal(withdrawn)
	}
	noerr(t, conn.Destroy())
	rxWant(t, e.p, conn.ID(), 0)

	// release is not a destructor: the device stays alive, and keeps
	// receiving events, until the compositor answers with released.
	noerr(t, dev.Release())
	rxWant(t, e.p, dev.ID(), 1)
	e.eventFD(dev.ID(), 2, nil)
	released := 0
	dev.OnReleased(func() { released++ })
	e.eventFD(dev.ID(), 3, nil)
	if released != 1 {
		t.Fatal(released)
	}
	if _, err := dev.CreateLeaseRequest(); err == nil {
		t.Fatal("request on a released device accepted")
	}
	requireNoWrite(t, e.p)
	send(t, e.p, msg(dev.ID(), 2))
	if err := e.d.Dispatch(); !errors.Is(err, wlturbo.ErrUnknownObject) {
		t.Fatalf("event after released = %v", err)
	}
}

func TestDRMLeaseDeviceWithoutDescriptorIsMalformed(t *testing.T) {
	e := rxNew(t)
	dev := drmlease.NewWpDrmLeaseDevice(e.d.Context())
	bind(t, e.d, e.p, 1, drmlease.WpDrmLeaseDeviceInterface, 1, 1, dev)
	called := false
	dev.OnDrmFd(func(*wlturbo.OwnedFD) { called = true })
	send(t, e.p, msg(dev.ID(), 0))
	if err := e.d.Dispatch(); !errors.Is(err, wlturbo.ErrMalformedFrame) {
		t.Fatalf("drm_fd without fd = %v", err)
	}
	if called {
		t.Fatal("handler ran without a descriptor")
	}
}

func TestSurfaceHintProtocolsWire(t *testing.T) {
	e := rxNew(t)

	// alpha modifier
	am := alphamodifier.NewWpAlphaModifier(e.d.Context())
	bind(t, e.d, e.p, 1, alphamodifier.WpAlphaModifierInterface, 1, 1, am)
	as, err := am.GetSurface(e.surface)
	noerr(t, err)
	rxWant(t, e.p, am.ID(), 1, as.ID(), e.surface.ID())
	noerr(t, as.SetMultiplier(0xffffffff))
	rxWant(t, e.p, as.ID(), 1, 0xffffffff)
	noerr(t, as.SetMultiplier(0x80000000))
	rxWant(t, e.p, as.ID(), 1, 0x80000000)
	// Destroying the manager leaves its children usable.
	noerr(t, am.Destroy())
	rxWant(t, e.p, am.ID(), 0)
	noerr(t, as.SetMultiplier(0))
	rxWant(t, e.p, as.ID(), 1, 0)
	noerr(t, as.Destroy())
	rxWant(t, e.p, as.ID(), 0)
	if err := as.SetMultiplier(1); err == nil {
		t.Fatal("request after destroy accepted")
	}
	requireNoWrite(t, e.p)

	// commit timing
	ct := committiming.NewWpCommitTimingManager(e.d.Context())
	bind(t, e.d, e.p, 2, committiming.WpCommitTimingManagerInterface, 1, 1, ct)
	timer, err := ct.GetTimer(e.surface)
	noerr(t, err)
	rxWant(t, e.p, ct.ID(), 1, timer.ID(), e.surface.ID())
	noerr(t, timer.SetTimestamp(1, 0xfffffffe, 999999999))
	rxWant(t, e.p, timer.ID(), 0, 1, 0xfffffffe, 999999999)
	noerr(t, timer.Destroy())
	rxWant(t, e.p, timer.ID(), 1)
	noerr(t, ct.Destroy())
	rxWant(t, e.p, ct.ID(), 0)

	// fifo: argument-less requests are bare 8-byte headers
	ff := fifo.NewWpFifoManager(e.d.Context())
	bind(t, e.d, e.p, 3, fifo.WpFifoManagerInterface, 1, 1, ff)
	f, err := ff.GetFifo(e.surface)
	noerr(t, err)
	rxWant(t, e.p, ff.ID(), 1, f.ID(), e.surface.ID())
	noerr(t, f.SetBarrier())
	rxWant(t, e.p, f.ID(), 0)
	noerr(t, f.WaitBarrier())
	rxWant(t, e.p, f.ID(), 1)
	noerr(t, f.Destroy())
	rxWant(t, e.p, f.ID(), 2)
	noerr(t, ff.Destroy())
	rxWant(t, e.p, ff.ID(), 0)

	// tearing control
	tc := tearingcontrol.NewWpTearingControlManager(e.d.Context())
	bind(t, e.d, e.p, 4, tearingcontrol.WpTearingControlManagerInterface, 1, 1, tc)
	tear, err := tc.GetTearingControl(e.surface)
	noerr(t, err)
	rxWant(t, e.p, tc.ID(), 1, tear.ID(), e.surface.ID())
	noerr(t, tear.SetPresentationHint(tearingcontrol.PRESENTATION_HINT_ASYNC))
	rxWant(t, e.p, tear.ID(), 0, 1)
	noerr(t, tear.SetPresentationHint(tearingcontrol.PRESENTATION_HINT_VSYNC))
	rxWant(t, e.p, tear.ID(), 0, 0)
	noerr(t, tear.Destroy())
	rxWant(t, e.p, tear.ID(), 1)
	noerr(t, tc.Destroy())
	rxWant(t, e.p, tc.ID(), 0)
}

func TestContentTypeSurfaceHintWire(t *testing.T) {
	e := rxNew(t)
	mgr := contenttype.NewWpContentTypeManager(e.d.Context())
	bind(t, e.d, e.p, 1, contenttype.WpContentTypeManagerInterface, 1, 1, mgr)
	ct, err := mgr.GetSurfaceContentType(e.surface)
	noerr(t, err)
	rxWant(t, e.p, mgr.ID(), 1, ct.ID(), e.surface.ID())
	for _, typ := range []uint32{contenttype.TYPE_GAME, contenttype.TYPE_VIDEO, contenttype.TYPE_PHOTO, contenttype.TYPE_NONE} {
		noerr(t, ct.SetContentType(typ))
		rxWant(t, e.p, ct.ID(), 1, typ)
	}
	// The manager may go first; its child keeps working.
	noerr(t, mgr.Destroy())
	rxWant(t, e.p, mgr.ID(), 0)
	noerr(t, ct.SetContentType(contenttype.TYPE_GAME))
	rxWant(t, e.p, ct.ID(), 1, 3)
	noerr(t, ct.Destroy())
	rxWant(t, e.p, ct.ID(), 0)
	if err := ct.SetContentType(1); err == nil {
		t.Fatal("request after destroy accepted")
	}
	requireNoWrite(t, e.p)
	// The interface has no events: any event is an unknown opcode.
	ct2 := contenttype.NewWpContentType(e.d.Context())
	ct2.SetID(e.d.AllocateID())
	e.d.Context().Register(ct2)
	send(t, e.p, msg(ct2.ID(), 0))
	if err := e.d.Dispatch(); !errors.Is(err, wlturbo.ErrUnknownOpcode) {
		t.Fatalf("event on eventless interface = %v", err)
	}
}

func TestXdgActivationTokenWireAndDoneEvent(t *testing.T) {
	e := rxNew(t)
	act := xdgactivation.NewXdgActivation(e.d.Context())
	bind(t, e.d, e.p, 1, xdgactivation.XdgActivationInterface, 1, 1, act)
	tok, err := act.GetActivationToken()
	noerr(t, err)
	rxWant(t, e.p, act.ID(), 1, tok.ID())
	noerr(t, tok.SetSerial(0x01020304, e.seat))
	rxWant(t, e.p, tok.ID(), 0, 0x01020304, e.seat.ID())
	noerr(t, tok.SetAppId("org.example.App"))
	rxWantRaw(t, e.p, tok.ID(), 1, str("org.example.App"))
	noerr(t, tok.SetSurface(e.surface))
	rxWant(t, e.p, tok.ID(), 2, e.surface.ID())
	noerr(t, tok.Commit())
	rxWant(t, e.p, tok.ID(), 3)

	var got string
	tok.OnDone(func(s string) { got = s })
	e.eventFD(tok.ID(), 0, str("activation-token-é"))
	if got != "activation-token-é" {
		t.Fatalf("done token %q", got)
	}
	// A string missing its NUL terminator is rejected before any handler.
	got = ""
	send(t, e.p, payload(tok.ID(), 0, arr([]byte("abc"))...))
	if err := e.d.Dispatch(); !errors.Is(err, wlturbo.ErrMalformedFrame) || got != "" {
		t.Fatalf("unterminated string = %v handler=%q", err, got)
	}

	noerr(t, act.Activate("activation-token-é", e.surface))
	rxWantRaw(t, e.p, act.ID(), 2, cat(str("activation-token-é"), wordBytes(e.surface.ID())))
	noerr(t, tok.Destroy())
	rxWant(t, e.p, tok.ID(), 4)
	noerr(t, act.Destroy())
	rxWant(t, e.p, act.ID(), 0)
}

func TestPointerWarpFixedWire(t *testing.T) {
	e := rxNew(t)
	pw := pointerwarp.NewWpPointerWarp(e.d.Context())
	bind(t, e.d, e.p, 1, pointerwarp.WpPointerWarpInterface, 1, 1, pw)
	noerr(t, pw.WarpPointer(e.surface, e.pointer, wlturbo.NewFixed(10.5), wlturbo.NewFixed(-3.25), 0xabcdef01))
	rxWant(t, e.p, pw.ID(), 1, e.surface.ID(), e.pointer.ID(), 2688, uint32(0xfffffcc0), 0xabcdef01)
	noerr(t, pw.Destroy())
	rxWant(t, e.p, pw.ID(), 0)
	if err := pw.WarpPointer(e.surface, e.pointer, 0, 0, 1); err == nil {
		t.Fatal("warp after destroy accepted")
	}
	requireNoWrite(t, e.p)
}

func TestColorRepresentationWireAndCapabilityEvents(t *testing.T) {
	e := rxNew(t)
	mgr := colorrepresentation.NewWpColorRepresentationManager(e.d.Context())
	bind(t, e.d, e.p, 1, colorrepresentation.WpColorRepresentationManagerInterface, 1, 1, mgr)

	var alphas []uint32
	var combos [][2]uint32
	done := 0
	mgr.OnSupportedAlphaMode(func(v uint32) { alphas = append(alphas, v) })
	mgr.OnSupportedCoefficientsAndRanges(func(c, r uint32) { combos = append(combos, [2]uint32{c, r}) })
	mgr.OnDone(func() { done++ })
	e.eventFD(mgr.ID(), 0, wordBytes(colorrepresentation.ALPHA_MODE_STRAIGHT))
	e.eventFD(mgr.ID(), 1, wordBytes(colorrepresentation.COEFFICIENTS_BT709, colorrepresentation.RANGE_LIMITED))
	e.eventFD(mgr.ID(), 1, wordBytes(colorrepresentation.COEFFICIENTS_ICTCP, colorrepresentation.RANGE_FULL))
	e.eventFD(mgr.ID(), 2, nil)
	if len(alphas) != 1 || alphas[0] != 2 || len(combos) != 2 || combos[0] != [2]uint32{2, 2} || combos[1] != [2]uint32{8, 1} || done != 1 {
		t.Fatalf("capabilities %v %v %d", alphas, combos, done)
	}

	s, err := mgr.GetSurface(e.surface)
	noerr(t, err)
	rxWant(t, e.p, mgr.ID(), 1, s.ID(), e.surface.ID())
	noerr(t, s.SetAlphaMode(colorrepresentation.ALPHA_MODE_PREMULTIPLIED_OPTICAL))
	rxWant(t, e.p, s.ID(), 1, 1)
	noerr(t, s.SetCoefficientsAndRange(colorrepresentation.COEFFICIENTS_BT2020, colorrepresentation.RANGE_LIMITED))
	rxWant(t, e.p, s.ID(), 2, 6, 2)
	noerr(t, s.SetChromaLocation(colorrepresentation.CHROMA_LOCATION_TYPE_5))
	rxWant(t, e.p, s.ID(), 3, 6)
	noerr(t, s.Destroy())
	rxWant(t, e.p, s.ID(), 0)
	noerr(t, mgr.Destroy())
	rxWant(t, e.p, mgr.ID(), 0)
}
