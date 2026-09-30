//go:build linux

package protocol_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/bnema/wlturbo"
	"github.com/bnema/wlturbo/protocol/core"
	"github.com/bnema/wlturbo/protocol/foreigntoplevel"
	"github.com/bnema/wlturbo/protocol/inputmethod"
	"github.com/bnema/wlturbo/protocol/kdeserverdecoration"
	"github.com/bnema/wlturbo/protocol/layershell"
	"github.com/bnema/wlturbo/protocol/outputmanagement"
	"github.com/bnema/wlturbo/protocol/outputpower"
	"github.com/bnema/wlturbo/protocol/screencopy"
	"github.com/bnema/wlturbo/protocol/textinput"
	"github.com/bnema/wlturbo/protocol/virtualkeyboard"
	"github.com/bnema/wlturbo/protocol/xdgshell"
	"golang.org/x/sys/unix"
)

// The output head, its modes and configuration objects are all created by
// server events (new_id in an event) or nested client requests.
func TestWlrOutputManagementHeadNewID(t *testing.T) {
	s := newWireEnv(t)
	mgr := outputmanagement.NewOutputManager(s.d.Context())
	s.bind(1, outputmanagement.OutputManagerInterface, 4, 4, mgr)

	var head *outputmanagement.OutputHead
	mgr.OnHead(func(h *outputmanagement.OutputHead) { head = h })
	var serial uint32
	mgr.OnDone(func(v uint32) { serial = v })
	s.event(mgr.ID(), 0, wordBytes(serverID+1)...)
	if head == nil || head.ID() != serverID+1 || head.Version() != mgr.Version() || head.Version() != 4 {
		t.Fatalf("head %+v", head)
	}

	var name string
	var mode *outputmanagement.OutputMode
	var current uint32
	var scale wlturbo.Fixed
	var enabled int32
	head.OnName(func(v string) { name = v })
	head.OnMode(func(m *outputmanagement.OutputMode) { mode = m })
	head.OnCurrentMode(func(id uint32) { current = id })
	head.OnScale(func(v wlturbo.Fixed) { scale = v })
	head.OnEnabled(func(v int32) { enabled = v })
	s.event(head.ID(), 0, str("HDMI-A-1")...)
	s.event(head.ID(), 3, wordBytes(serverID+2)...)
	if mode == nil || mode.ID() != serverID+2 || mode.Version() != 4 {
		t.Fatalf("mode %+v", mode)
	}
	var w, h, refresh int32
	var preferred bool
	mode.OnSize(func(a, b int32) { w, h = a, b })
	mode.OnRefresh(func(v int32) { refresh = v })
	mode.OnPreferred(func() { preferred = true })
	s.event(mode.ID(), 0, wordBytes(1920, 1080)...)
	s.event(mode.ID(), 1, wordBytes(60000)...)
	s.event(mode.ID(), 2)
	s.event(head.ID(), 4, wordBytes(1)...)
	s.event(head.ID(), 5, wordBytes(mode.ID())...)
	s.event(head.ID(), 8, wordBytes(uint32(wlturbo.NewFixed(1.5)))...)
	s.event(mgr.ID(), 1, wordBytes(41)...)
	if name != "HDMI-A-1" || w != 1920 || h != 1080 || refresh != 60000 || !preferred ||
		enabled != 1 || current != mode.ID() || scale.Float64() != 1.5 || serial != 41 {
		t.Fatal(name, w, h, refresh, preferred, enabled, current, scale, serial)
	}

	// create_configuration -> enable_head (new_id + object) -> setters -> apply.
	cfg, e := mgr.CreateConfiguration(serial)
	noerr(t, e)
	wantWords(t, "create_configuration", s.req(mgr.ID(), 0), cfg.ID(), 41)
	ch, e := cfg.EnableHead(head)
	noerr(t, e)
	wantWords(t, "enable_head", s.req(cfg.ID(), 0), ch.ID(), head.ID())
	noerr(t, ch.SetMode(mode))
	wantWords(t, "set_mode", s.req(ch.ID(), 0), mode.ID())
	noerr(t, ch.SetPosition(-10, 20))
	wantWords(t, "set_position", s.req(ch.ID(), 2), 0xfffffff6, 20)
	noerr(t, ch.SetTransform(core.TRANSFORM_90))
	wantWords(t, "set_transform", s.req(ch.ID(), 3), core.TRANSFORM_90)
	noerr(t, ch.SetScale(wlturbo.NewFixed(2)))
	wantWords(t, "set_scale", s.req(ch.ID(), 4), uint32(wlturbo.NewFixed(2)))
	noerr(t, ch.SetAdaptiveSync(outputmanagement.ADAPTIVE_SYNC_STATE_ENABLED))
	wantWords(t, "set_adaptive_sync", s.req(ch.ID(), 5), 1)
	noerr(t, cfg.Test())
	s.req(cfg.ID(), 3)
	noerr(t, cfg.Apply())
	s.req(cfg.ID(), 2)
	var results []string
	cfg.OnSucceeded(func() { results = append(results, "succeeded") })
	cfg.OnFailed(func() { results = append(results, "failed") })
	cfg.OnCancelled(func() { results = append(results, "cancelled") })
	s.event(cfg.ID(), 0)
	s.event(cfg.ID(), 1)
	s.event(cfg.ID(), 2)
	if !reflect.DeepEqual(results, []string{"succeeded", "failed", "cancelled"}) {
		t.Fatal(results)
	}
	noerr(t, cfg.Destroy())
	s.req(cfg.ID(), 4)

	// Release destructors (since 3) and manager finished (destructor event).
	var headFinished bool
	head.OnFinished(func() { headFinished = true })
	s.event(head.ID(), 9)
	if !headFinished {
		t.Fatal("head finished not delivered")
	}
	noerr(t, mode.Release())
	s.req(mode.ID(), 0)
	noerr(t, head.Release())
	s.req(head.ID(), 0)
	noerr(t, mgr.Stop())
	s.req(mgr.ID(), 1)
	var finished bool
	mgr.OnFinished(func() { finished = true })
	s.event(mgr.ID(), 2)
	if !finished {
		t.Fatal("finished not delivered")
	}
	if e := mgr.Stop(); e == nil {
		t.Fatal("request on a finished manager must fail")
	}
	s.noRequest()
}

func TestWlrOutputManagementVersionGate(t *testing.T) {
	s := newWireEnv(t)
	mgr := outputmanagement.NewOutputManager(s.d.Context())
	s.bind(1, outputmanagement.OutputManagerInterface, 4, 2, mgr)
	var head *outputmanagement.OutputHead
	mgr.OnHead(func(h *outputmanagement.OutputHead) { head = h })
	s.event(mgr.ID(), 0, wordBytes(serverID+1)...)
	if head == nil || head.Version() != 2 {
		t.Fatalf("head %+v", head)
	}
	if e := head.Release(); !errors.Is(e, wlturbo.ErrVersionTooLow) {
		t.Fatalf("release on v2 head: %v", e)
	}
	s.noRequest()
}

func TestWlrScreencopyBufferFlow(t *testing.T) {
	s := newWireEnv(t)
	out := s.output()
	shm := core.NewShm(s.d.Context())
	s.bind(1, core.ShmInterface, 1, 1, shm)
	mgr := screencopy.NewScreencopyManager(s.d.Context())
	s.bind(2, screencopy.ScreencopyManagerInterface, 3, 3, mgr)

	frame, e := mgr.CaptureOutput(1, out)
	noerr(t, e)
	wantWords(t, "capture_output", s.req(mgr.ID(), 0), frame.ID(), 1, out.ID())
	if frame.Version() != 3 {
		t.Fatalf("frame version %d", frame.Version())
	}
	var events []string
	var shmW, shmH, shmStride, shmFormat uint32
	frame.OnBuffer(func(f, w, h, st uint32) {
		shmFormat, shmW, shmH, shmStride = f, w, h, st
		events = append(events, "buffer")
	})
	frame.OnLinuxDmabuf(func(f, w, h uint32) { events = append(events, "dmabuf") })
	frame.OnBufferDone(func() { events = append(events, "done") })
	frame.OnFlags(func(f uint32) {
		if f != screencopy.FLAGS_Y_INVERT {
			t.Fatalf("flags %d", f)
		}
		events = append(events, "flags")
	})
	var sec uint64
	var nsec uint32
	frame.OnReady(func(hi, lo, ns uint32) { sec, nsec = uint64(hi)<<32|uint64(lo), ns; events = append(events, "ready") })
	s.event(frame.ID(), 0, wordBytes(core.FORMAT_XRGB8888, 4, 2, 16)...)
	s.event(frame.ID(), 5, wordBytes(0x34325258, 4, 2)...)
	s.event(frame.ID(), 6)
	if shmFormat != core.FORMAT_XRGB8888 || shmW != 4 || shmH != 2 || shmStride != 16 {
		t.Fatal(shmFormat, shmW, shmH, shmStride)
	}

	// Build the wl_shm buffer the compositor asked for; the pool descriptor is
	// duplicated by the kernel and the client's copy is closed by the binding.
	memfd, e := unix.MemfdCreate("wlturbo-shm", unix.MFD_CLOEXEC)
	noerr(t, e)
	noerr(t, unix.Ftruncate(memfd, 32))
	pool, e := shm.CreatePool(memfd, 32)
	noerr(t, e)
	requireFDClosed(t, memfd)
	body, fd := s.reqFD(shm.ID(), 0)
	unix.Close(fd)
	wantWords(t, "create_pool", body, pool.ID(), 32)
	buf, e := pool.CreateBuffer(0, 4, 2, 16, core.FORMAT_XRGB8888)
	noerr(t, e)
	wantWords(t, "create_buffer", s.req(pool.ID(), 0), buf.ID(), 0, 4, 2, 16, core.FORMAT_XRGB8888)

	noerr(t, frame.Copy(buf))
	wantWords(t, "copy", s.req(frame.ID(), 0), buf.ID())
	s.event(frame.ID(), 1, wordBytes(screencopy.FLAGS_Y_INVERT)...)
	s.event(frame.ID(), 2, wordBytes(1, 2, 999999999)...)
	if sec != 1<<32|2 || nsec != 999999999 {
		t.Fatal(sec, nsec)
	}
	want := []string{"buffer", "dmabuf", "done", "flags", "ready"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events %v, want %v", events, want)
	}
	noerr(t, frame.Destroy())
	s.req(frame.ID(), 1)

	// Region capture + damage + failure on a second frame.
	f2, e := mgr.CaptureOutputRegion(0, out, -1, 2, 30, 40)
	noerr(t, e)
	wantWords(t, "capture_output_region", s.req(mgr.ID(), 1), f2.ID(), 0, out.ID(), 0xffffffff, 2, 30, 40)
	noerr(t, f2.CopyWithDamage(buf))
	wantWords(t, "copy_with_damage", s.req(f2.ID(), 2), buf.ID())
	var damage []uint32
	var failed bool
	f2.OnDamage(func(x, y, w, h uint32) { damage = []uint32{x, y, w, h} })
	f2.OnFailed(func() { failed = true })
	s.event(f2.ID(), 4, wordBytes(1, 2, 3, 4)...)
	s.event(f2.ID(), 3)
	if !reflect.DeepEqual(damage, []uint32{1, 2, 3, 4}) || !failed {
		t.Fatal(damage, failed)
	}
	noerr(t, f2.Destroy())
	s.req(f2.ID(), 1)
	noerr(t, buf.Destroy())
	s.req(buf.ID(), 0)
	noerr(t, mgr.Destroy())
	s.req(mgr.ID(), 2)
}

func TestWlrScreencopyVersionGate(t *testing.T) {
	s := newWireEnv(t)
	out := s.output()
	mgr := screencopy.NewScreencopyManager(s.d.Context())
	s.bind(1, screencopy.ScreencopyManagerInterface, 3, 1, mgr)
	frame, e := mgr.CaptureOutput(0, out)
	noerr(t, e)
	s.req(mgr.ID(), 0)
	buf := core.NewBuffer(s.d.Context())
	buf.SetID(s.d.AllocateID())
	s.d.Context().Register(buf)
	if e := frame.CopyWithDamage(buf); !errors.Is(e, wlturbo.ErrVersionTooLow) {
		t.Fatalf("copy_with_damage on v1 frame: %v", e)
	}
	s.noRequest()
	noerr(t, frame.Copy(buf))
	s.req(frame.ID(), 0)
}

func TestWlrInputMethodFDAndCrossPackageEnums(t *testing.T) {
	s := newWireEnv(t)
	seat, surf := s.seat(), s.surface()
	mgr := inputmethod.NewInputMethodManager(s.d.Context())
	s.bind(1, inputmethod.InputMethodManagerInterface, 1, 1, mgr)
	im, e := mgr.GetInputMethod(seat)
	noerr(t, e)
	wantWords(t, "get_input_method", s.req(mgr.ID(), 0), seat.ID(), im.ID())

	var seq []string
	var text string
	var cursor, anchor, cause, hint, purpose uint32
	im.OnActivate(func() { seq = append(seq, "activate") })
	im.OnSurroundingText(func(tx string, c, a uint32) { text, cursor, anchor = tx, c, a; seq = append(seq, "surrounding") })
	im.OnTextChangeCause(func(c uint32) { cause = c; seq = append(seq, "cause") })
	im.OnContentType(func(h, p uint32) { hint, purpose = h, p; seq = append(seq, "content") })
	im.OnDone(func() { seq = append(seq, "done") })
	s.event(im.ID(), 0)
	s.event(im.ID(), 2, cat(str("héllo"), wordBytes(3, 3))...)
	s.event(im.ID(), 3, wordBytes(textinput.CHANGE_CAUSE_OTHER)...)
	s.event(im.ID(), 4, wordBytes(textinput.CONTENT_HINT_SPELLCHECK, textinput.CONTENT_PURPOSE_EMAIL)...)
	s.event(im.ID(), 5)
	if !reflect.DeepEqual(seq, []string{"activate", "surrounding", "cause", "content", "done"}) ||
		text != "héllo" || cursor != 3 || anchor != 3 || cause != textinput.CHANGE_CAUSE_OTHER ||
		hint != textinput.CONTENT_HINT_SPELLCHECK || purpose != textinput.CONTENT_PURPOSE_EMAIL {
		t.Fatal(seq, text, cursor, anchor, cause, hint, purpose)
	}

	noerr(t, im.CommitString("ok"))
	if b := s.req(im.ID(), 0); !bytes.Equal(b, str("ok")) {
		t.Fatalf("commit_string %x", b)
	}
	noerr(t, im.SetPreeditString("pre", -1, -1))
	if b := s.req(im.ID(), 1); !bytes.Equal(b, cat(str("pre"), wordBytes(0xffffffff, 0xffffffff))) {
		t.Fatalf("set_preedit_string %x", b)
	}
	noerr(t, im.DeleteSurroundingText(2, 1))
	wantWords(t, "delete_surrounding_text", s.req(im.ID(), 2), 2, 1)
	noerr(t, im.Commit(1))
	wantWords(t, "commit", s.req(im.ID(), 3), 1)

	popup, e := im.GetInputPopupSurface(surf)
	noerr(t, e)
	wantWords(t, "get_input_popup_surface", s.req(im.ID(), 4), popup.ID(), surf.ID())
	var rect []int32
	popup.OnTextInputRectangle(func(x, y, w, h int32) { rect = []int32{x, y, w, h} })
	s.event(popup.ID(), 0, wordBytes(0xfffffffe, 5, 30, 12)...)
	if !reflect.DeepEqual(rect, []int32{-2, 5, 30, 12}) {
		t.Fatal(rect)
	}

	grab, e := im.GrabKeyboard()
	noerr(t, e)
	wantWords(t, "grab_keyboard", s.req(im.ID(), 5), grab.ID())

	// keymap carries a descriptor: the first handler may take ownership, the
	// second sees nil, and an unclaimed descriptor is closed by the transport.
	var claim = true
	var first, second []*wlturbo.OwnedFD
	var format, size uint32
	var got []byte
	grab.OnKeymap(func(f uint32, fd *wlturbo.OwnedFD, sz uint32) {
		first = append(first, fd)
		format, size = f, sz
		if !claim {
			return
		}
		n, e := fd.Take()
		noerr(t, e)
		buf := make([]byte, 16)
		m, e := unix.Read(n, buf)
		noerr(t, e)
		got = buf[:m]
		unix.Close(n)
	})
	grab.OnKeymap(func(f uint32, fd *wlturbo.OwnedFD, sz uint32) { second = append(second, fd) })
	pr, pw, e := os.Pipe()
	noerr(t, e)
	_, e = pw.Write([]byte("xkb-keymap"))
	noerr(t, e)
	send(t, s.p, payload(grab.ID(), 0, wordBytes(core.KEYMAP_FORMAT_XKB_V1, 10)...), int(pr.Fd()))
	pr.Close()
	pw.Close()
	s.dispatch()
	if string(got) != "xkb-keymap" || format != core.KEYMAP_FORMAT_XKB_V1 || size != 10 || len(second) != 1 || second[0] != nil {
		t.Fatalf("keymap got=%q format=%d size=%d second=%v", got, format, size, second)
	}

	claim = false
	pr2, pw2, e := os.Pipe()
	noerr(t, e)
	defer pr2.Close()
	send(t, s.p, payload(grab.ID(), 0, wordBytes(core.KEYMAP_FORMAT_XKB_V1, 0)...), int(pw2.Fd()))
	pw2.Close() // only the in-flight duplicate keeps the write end alive
	s.dispatch()
	pr2.SetReadDeadline(time.Now().Add(2 * time.Second))
	if n, e := pr2.Read(make([]byte, 4)); n != 0 || e == nil {
		t.Fatalf("unclaimed descriptor still open: n=%d err=%v", n, e)
	}

	var key, mods, repeat []uint32
	grab.OnKey(func(serial, tm, k, st uint32) { key = []uint32{serial, tm, k, st} })
	grab.OnModifiers(func(serial, d, l, lk, g uint32) { mods = []uint32{serial, d, l, lk, g} })
	grab.OnRepeatInfo(func(rate, delay int32) { repeat = []uint32{uint32(rate), uint32(delay)} })
	s.event(grab.ID(), 1, wordBytes(7, 100, 30, core.KEY_STATE_PRESSED)...)
	s.event(grab.ID(), 2, wordBytes(8, 1, 2, 4, 1)...)
	s.event(grab.ID(), 3, wordBytes(25, 600)...)
	if !reflect.DeepEqual(key, []uint32{7, 100, 30, core.KEY_STATE_PRESSED}) ||
		!reflect.DeepEqual(mods, []uint32{8, 1, 2, 4, 1}) || !reflect.DeepEqual(repeat, []uint32{25, 600}) {
		t.Fatal(key, mods, repeat)
	}

	noerr(t, grab.Release())
	s.req(grab.ID(), 0)
	noerr(t, popup.Destroy())
	s.req(popup.ID(), 0)
	var unavailable bool
	im.OnUnavailable(func() { unavailable = true })
	s.event(im.ID(), 6)
	if !unavailable {
		t.Fatal("unavailable not delivered")
	}
	noerr(t, im.Destroy())
	s.req(im.ID(), 6)
	noerr(t, mgr.Destroy())
	s.req(mgr.ID(), 1)
}

func TestWlrVirtualKeyboardKeymapFD(t *testing.T) {
	s := newWireEnv(t)
	seat := s.seat()
	mgr := virtualkeyboard.NewVirtualKeyboardManager(s.d.Context())
	s.bind(1, virtualkeyboard.VirtualKeyboardManagerInterface, 1, 1, mgr)
	kb, e := mgr.CreateVirtualKeyboard(seat)
	noerr(t, e)
	wantWords(t, "create_virtual_keyboard", s.req(mgr.ID(), 0), seat.ID(), kb.ID())

	r, w, e := os.Pipe()
	noerr(t, e)
	defer r.Close()
	// Hand the write end to the binding as a plain descriptor.
	fd, e := unix.Dup(int(w.Fd()))
	noerr(t, e)
	w.Close()
	noerr(t, kb.Keymap(core.KEYMAP_FORMAT_XKB_V1, fd, 6))
	requireFDClosed(t, fd)
	body, got := s.reqFD(kb.ID(), 0)
	wantWords(t, "keymap", body, core.KEYMAP_FORMAT_XKB_V1, 6)
	_, e = unix.Write(got, []byte("keymap"))
	noerr(t, e)
	unix.Close(got)
	buf := make([]byte, 16)
	r.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, e := r.Read(buf)
	noerr(t, e)
	if string(buf[:n]) != "keymap" {
		t.Fatalf("keymap payload %q", buf[:n])
	}

	noerr(t, kb.Key(1234, 30, core.KEY_STATE_PRESSED))
	wantWords(t, "key", s.req(kb.ID(), 1), 1234, 30, core.KEY_STATE_PRESSED)
	noerr(t, kb.Modifiers(1, 2, 4, 0))
	wantWords(t, "modifiers", s.req(kb.ID(), 2), 1, 2, 4, 0)
	noerr(t, kb.Destroy())
	s.req(kb.ID(), 3)
	if e := kb.Key(1, 1, 0); e == nil {
		t.Fatal("request after destroy must fail")
	}
	s.noRequest()
}

// A layer surface is a wl_surface role defined in layershell, and its popup is
// an xdg_popup from xdgshell; both packages must agree on the same objects.
func TestWlrLayerShellPopupCrossPackage(t *testing.T) {
	s := newWireEnv(t)
	panel, popupSurf := s.surface(), s.surface()
	out := s.output()

	wm := xdgshell.NewXdgWmBase(s.d.Context())
	s.bind(1, xdgshell.XdgWmBaseInterface, 6, 6, wm)
	ls := layershell.NewLayerShell(s.d.Context())
	s.bind(2, layershell.LayerShellInterface, 5, 5, ls)

	surf, e := ls.GetLayerSurface(panel, out, layershell.LAYER_TOP, "panel")
	noerr(t, e)
	if b := s.req(ls.ID(), 0); !bytes.Equal(b, cat(wordBytes(surf.ID(), panel.ID(), out.ID(), layershell.LAYER_TOP), str("panel"))) {
		t.Fatalf("get_layer_surface %x", b)
	}
	if surf.Version() != 5 {
		t.Fatalf("layer surface version %d", surf.Version())
	}
	// A nullable output is encoded as object 0.
	nullOut, e := ls.GetLayerSurface(s.surface(), nil, layershell.LAYER_BACKGROUND, "wallpaper")
	noerr(t, e)
	if b := s.req(ls.ID(), 0); binary.NativeEndian.Uint32(b[8:]) != 0 || binary.NativeEndian.Uint32(b[:4]) != nullOut.ID() {
		t.Fatalf("null output %x", b)
	}

	anchors := uint32(layershell.ANCHOR_TOP | layershell.ANCHOR_LEFT | layershell.ANCHOR_RIGHT)
	noerr(t, surf.SetSize(0, 30))
	wantWords(t, "set_size", s.req(surf.ID(), 0), 0, 30)
	noerr(t, surf.SetAnchor(anchors))
	wantWords(t, "set_anchor", s.req(surf.ID(), 1), anchors)
	noerr(t, surf.SetExclusiveZone(-1))
	wantWords(t, "set_exclusive_zone", s.req(surf.ID(), 2), 0xffffffff)
	noerr(t, surf.SetMargin(1, 2, 3, 4))
	wantWords(t, "set_margin", s.req(surf.ID(), 3), 1, 2, 3, 4)
	noerr(t, surf.SetKeyboardInteractivity(layershell.KEYBOARD_INTERACTIVITY_ON_DEMAND))
	wantWords(t, "set_keyboard_interactivity", s.req(surf.ID(), 4), layershell.KEYBOARD_INTERACTIVITY_ON_DEMAND)
	noerr(t, surf.SetLayer(layershell.LAYER_OVERLAY))
	wantWords(t, "set_layer", s.req(surf.ID(), 8), layershell.LAYER_OVERLAY)
	noerr(t, surf.SetExclusiveEdge(layershell.ANCHOR_TOP))
	wantWords(t, "set_exclusive_edge", s.req(surf.ID(), 9), layershell.ANCHOR_TOP)

	var cfgSerial, cfgW, cfgH uint32
	surf.OnConfigure(func(serial, w, h uint32) {
		cfgSerial, cfgW, cfgH = serial, w, h
		noerr(t, surf.AckConfigure(serial))
	})
	s.event(surf.ID(), 0, wordBytes(77, 1920, 30)...)
	if cfgSerial != 77 || cfgW != 1920 || cfgH != 30 {
		t.Fatal(cfgSerial, cfgW, cfgH)
	}
	wantWords(t, "ack_configure", s.req(surf.ID(), 6), 77)

	// The popup is created through xdg_shell with a NULL parent, then adopted
	// by the layer surface.
	xs, e := wm.GetXdgSurface(popupSurf)
	noerr(t, e)
	s.req(wm.ID(), 2)
	pos, e := wm.CreatePositioner()
	noerr(t, e)
	s.req(wm.ID(), 1)
	noerr(t, pos.SetSize(100, 50))
	wantWords(t, "positioner size", s.req(pos.ID(), 1), 100, 50)
	noerr(t, pos.SetAnchorRect(0, 0, 10, 30))
	s.req(pos.ID(), 2)
	popup, e := xs.GetPopup(nil, pos)
	noerr(t, e)
	wantWords(t, "get_popup", s.req(xs.ID(), 2), popup.ID(), 0, pos.ID())
	noerr(t, surf.GetPopup(popup))
	wantWords(t, "layer get_popup", s.req(surf.ID(), 5), popup.ID())

	var popupCfg []int32
	var done bool
	popup.OnConfigure(func(x, y, w, h int32) { popupCfg = []int32{x, y, w, h} })
	popup.OnPopupDone(func() { done = true })
	s.event(popup.ID(), 0, wordBytes(4, 30, 100, 50)...)
	s.event(popup.ID(), 1)
	if !reflect.DeepEqual(popupCfg, []int32{4, 30, 100, 50}) || !done {
		t.Fatal(popupCfg, done)
	}

	var closed bool
	surf.OnClosed(func() { closed = true })
	s.event(surf.ID(), 1)
	if !closed {
		t.Fatal("closed not delivered")
	}
	noerr(t, popup.Destroy())
	s.req(popup.ID(), 0)
	noerr(t, surf.Destroy())
	s.req(surf.ID(), 7)
	noerr(t, ls.Destroy())
	s.req(ls.ID(), 1)
	if e := surf.SetSize(1, 1); e == nil {
		t.Fatal("request after destroy must fail")
	}
	s.noRequest()
}

func TestWlrLayerShellVersionGate(t *testing.T) {
	s := newWireEnv(t)
	ls := layershell.NewLayerShell(s.d.Context())
	s.bind(1, layershell.LayerShellInterface, 5, 4, ls)
	surf, e := ls.GetLayerSurface(s.surface(), nil, layershell.LAYER_TOP, "x")
	noerr(t, e)
	s.req(ls.ID(), 0)
	if e := surf.SetExclusiveEdge(layershell.ANCHOR_TOP); !errors.Is(e, wlturbo.ErrVersionTooLow) {
		t.Fatalf("set_exclusive_edge on v4: %v", e)
	}
	s.noRequest()
	noerr(t, surf.SetLayer(layershell.LAYER_BOTTOM))
	s.req(surf.ID(), 8)
}

func TestWlrForeignToplevelOutputPowerAndKDEDecoration(t *testing.T) {
	s := newWireEnv(t)
	seat, surf, out := s.seat(), s.surface(), s.output()

	// foreign-toplevel: toplevel is a server new_id event; finished destroys.
	tm := foreigntoplevel.NewForeignToplevelManager(s.d.Context())
	s.bind(1, foreigntoplevel.ForeignToplevelManagerInterface, 3, 3, tm)
	var top *foreigntoplevel.ForeignToplevelHandle
	tm.OnToplevel(func(h *foreigntoplevel.ForeignToplevelHandle) { top = h })
	s.event(tm.ID(), 0, wordBytes(serverID+1)...)
	if top == nil || top.ID() != serverID+1 || top.Version() != 3 {
		t.Fatalf("toplevel %+v", top)
	}
	var title, appID string
	var states []byte
	var enter, parent uint32
	var done, closed bool
	top.OnTitle(func(v string) { title = v })
	top.OnAppId(func(v string) { appID = v })
	top.OnState(func(v []byte) { states = append([]byte(nil), v...) })
	top.OnOutputEnter(func(id uint32) { enter = id })
	top.OnParent(func(id uint32) { parent = id })
	top.OnDone(func() { done = true })
	top.OnClosed(func() { closed = true })
	s.event(top.ID(), 0, str("term")...)
	s.event(top.ID(), 1, str("org.example.term")...)
	s.event(top.ID(), 2, wordBytes(out.ID())...)
	s.event(top.ID(), 4, arr(wordBytes(foreigntoplevel.STATE_MAXIMIZED, foreigntoplevel.STATE_ACTIVATED))...)
	s.event(top.ID(), 7, wordBytes(0)...)
	s.event(top.ID(), 5)
	if title != "term" || appID != "org.example.term" || enter != out.ID() || parent != 0 || !done ||
		!bytes.Equal(states, wordBytes(foreigntoplevel.STATE_MAXIMIZED, foreigntoplevel.STATE_ACTIVATED)) {
		t.Fatal(title, appID, enter, parent, done, states)
	}
	noerr(t, top.SetMaximized())
	s.req(top.ID(), 0)
	noerr(t, top.Activate(seat))
	wantWords(t, "activate", s.req(top.ID(), 4), seat.ID())
	noerr(t, top.SetRectangle(surf, 1, 2, 3, 4))
	wantWords(t, "set_rectangle", s.req(top.ID(), 6), surf.ID(), 1, 2, 3, 4)
	noerr(t, top.SetFullscreen(nil))
	wantWords(t, "set_fullscreen(nil)", s.req(top.ID(), 8), 0)
	noerr(t, top.SetFullscreen(out))
	wantWords(t, "set_fullscreen", s.req(top.ID(), 8), out.ID())
	noerr(t, top.Close())
	s.req(top.ID(), 5)
	s.event(top.ID(), 6)
	if !closed {
		t.Fatal("closed not delivered")
	}
	noerr(t, top.Destroy())
	s.req(top.ID(), 7)
	noerr(t, tm.Stop())
	s.req(tm.ID(), 0)
	s.event(tm.ID(), 1)
	if e := tm.Stop(); e == nil {
		t.Fatal("request on a finished manager must fail")
	}

	// output-power: object argument + mode/failed events.
	pm := outputpower.NewOutputPowerManager(s.d.Context())
	s.bind(2, outputpower.OutputPowerManagerInterface, 1, 1, pm)
	pw, e := pm.GetOutputPower(out)
	noerr(t, e)
	wantWords(t, "get_output_power", s.req(pm.ID(), 0), pw.ID(), out.ID())
	var modes []uint32
	var failed bool
	pw.OnMode(func(m uint32) { modes = append(modes, m) })
	pw.OnFailed(func() { failed = true })
	s.event(pw.ID(), 0, wordBytes(outputpower.MODE_ON)...)
	noerr(t, pw.SetMode(outputpower.MODE_OFF))
	wantWords(t, "set_mode", s.req(pw.ID(), 0), outputpower.MODE_OFF)
	s.event(pw.ID(), 0, wordBytes(outputpower.MODE_OFF)...)
	s.event(pw.ID(), 1)
	if !reflect.DeepEqual(modes, []uint32{outputpower.MODE_ON, outputpower.MODE_OFF}) || !failed {
		t.Fatal(modes, failed)
	}
	noerr(t, pw.Destroy())
	s.req(pw.ID(), 1)
	noerr(t, pm.Destroy())
	s.req(pm.ID(), 1)

	// KDE server decoration: mode enums are per-interface constants.
	dm := kdeserverdecoration.NewOrgKdeKwinServerDecorationManager(s.d.Context())
	s.bind(3, kdeserverdecoration.OrgKdeKwinServerDecorationManagerInterface, 1, 1, dm)
	var def uint32 = 99
	dm.OnDefaultMode(func(m uint32) { def = m })
	s.event(dm.ID(), 0, wordBytes(kdeserverdecoration.ORG_KDE_KWIN_SERVER_DECORATION_MANAGER_MODE_CLIENT)...)
	if def != kdeserverdecoration.ORG_KDE_KWIN_SERVER_DECORATION_MANAGER_MODE_CLIENT {
		t.Fatal(def)
	}
	dec, e := dm.Create(surf)
	noerr(t, e)
	wantWords(t, "create", s.req(dm.ID(), 0), dec.ID(), surf.ID())
	var mode uint32 = 99
	dec.OnMode(func(m uint32) { mode = m })
	noerr(t, dec.RequestMode(kdeserverdecoration.ORG_KDE_KWIN_SERVER_DECORATION_MODE_SERVER))
	wantWords(t, "request_mode", s.req(dec.ID(), 1), kdeserverdecoration.ORG_KDE_KWIN_SERVER_DECORATION_MODE_SERVER)
	s.event(dec.ID(), 0, wordBytes(kdeserverdecoration.ORG_KDE_KWIN_SERVER_DECORATION_MODE_SERVER)...)
	if mode != kdeserverdecoration.ORG_KDE_KWIN_SERVER_DECORATION_MODE_SERVER {
		t.Fatal(mode)
	}
	noerr(t, dec.Release())
	s.req(dec.ID(), 0)
	s.noRequest()
}
