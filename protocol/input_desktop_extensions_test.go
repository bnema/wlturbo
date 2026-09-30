//go:build linux

package protocol_test

import (
	"encoding/binary"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/bnema/wlturbo"
	"github.com/bnema/wlturbo/protocol/datacontrol"
	"github.com/bnema/wlturbo/protocol/extforeigntoplevel"
	"github.com/bnema/wlturbo/protocol/idleinhibit"
	"github.com/bnema/wlturbo/protocol/idlenotify"
	"github.com/bnema/wlturbo/protocol/imagecapturesource"
	"github.com/bnema/wlturbo/protocol/imagecopycapture"
	"github.com/bnema/wlturbo/protocol/pointerconstraints"
	"github.com/bnema/wlturbo/protocol/primaryselection"
	"github.com/bnema/wlturbo/protocol/relativepointer"
	"github.com/bnema/wlturbo/protocol/shortcutsinhibit"
	"github.com/bnema/wlturbo/protocol/workspace"
	"github.com/bnema/wlturbo/protocol/xdgdecoration"
	"github.com/bnema/wlturbo/protocol/xdgoutput"
	"github.com/bnema/wlturbo/protocol/xdgshell"
	"golang.org/x/sys/unix"
)

func TestRelativePointerMotionDecode(t *testing.T) {
	s := newWireEnv(t)
	ptr := s.pointer()
	mgr := relativepointer.NewRelativePointerManager(s.d.Context())
	s.bind(1, relativepointer.RelativePointerManagerInterface, 1, 1, mgr)
	rp, e := mgr.GetRelativePointer(ptr)
	noerr(t, e)
	wantWords(t, "get_relative_pointer", s.req(mgr.ID(), 1), rp.ID(), ptr.ID())

	type motion struct {
		hi, lo         uint32
		dx, dy, ux, uy wlturbo.Fixed
	}
	var got []motion
	rp.OnRelativeMotion(func(hi, lo uint32, dx, dy, ux, uy wlturbo.Fixed) {
		got = append(got, motion{hi, lo, dx, dy, ux, uy})
	})
	// 64-bit microsecond timestamp split in two words; negative deltas must
	// keep their sign through the 24.8 fixed-point decode.
	want := motion{1, 0x89abcdef, wlturbo.NewFixed(-1.5), wlturbo.NewFixed(2.25), wlturbo.NewFixed(-3), wlturbo.NewFixed(0.5)}
	s.event(rp.ID(), 0, wordBytes(want.hi, want.lo, uint32(want.dx), uint32(want.dy), uint32(want.ux), uint32(want.uy))...)
	if len(got) != 1 || got[0] != want {
		t.Fatalf("motion %+v, want %+v", got, want)
	}
	if got[0].dx.Float64() != -1.5 || got[0].dy.Float64() != 2.25 {
		t.Fatalf("fixed decode %v %v", got[0].dx.Float64(), got[0].dy.Float64())
	}
	// Two handlers are both called, in registration order.
	var order []int
	rp.OnRelativeMotion(func(_, _ uint32, _, _, _, _ wlturbo.Fixed) { order = append(order, 1) })
	rp.OnRelativeMotion(func(_, _ uint32, _, _, _, _ wlturbo.Fixed) { order = append(order, 2) })
	s.event(rp.ID(), 0, wordBytes(0, 1, 0, 0, 0, 0)...)
	if !reflect.DeepEqual(order, []int{1, 2}) || len(got) != 2 {
		t.Fatalf("handlers order=%v motions=%d", order, len(got))
	}
	noerr(t, rp.Destroy())
	s.req(rp.ID(), 0)
	noerr(t, mgr.Destroy())
	s.req(mgr.ID(), 0)
}

func TestPointerConstraintsNullableRegion(t *testing.T) {
	s := newWireEnv(t)
	surf, ptr := s.surface(), s.pointer()
	pc := pointerconstraints.NewPointerConstraints(s.d.Context())
	s.bind(1, pointerconstraints.PointerConstraintsInterface, 1, 1, pc)

	// A null region means "use the surface input region" and is wire id 0.
	lock, e := pc.LockPointer(surf, ptr, nil, pointerconstraints.LIFETIME_PERSISTENT)
	noerr(t, e)
	wantWords(t, "lock_pointer(nil)", s.req(pc.ID(), 1), lock.ID(), surf.ID(), ptr.ID(), 0, pointerconstraints.LIFETIME_PERSISTENT)

	reg := s.region()
	conf, e := pc.ConfinePointer(surf, ptr, reg, pointerconstraints.LIFETIME_ONESHOT)
	noerr(t, e)
	wantWords(t, "confine_pointer(region)", s.req(pc.ID(), 2), conf.ID(), surf.ID(), ptr.ID(), reg.ID(), pointerconstraints.LIFETIME_ONESHOT)

	noerr(t, lock.SetCursorPositionHint(wlturbo.NewFixed(10.5), wlturbo.NewFixed(-2)))
	wantWords(t, "set_cursor_position_hint", s.req(lock.ID(), 1), uint32(wlturbo.NewFixed(10.5)), uint32(wlturbo.NewFixed(-2)))
	noerr(t, lock.SetRegion(nil))
	wantWords(t, "locked.set_region(nil)", s.req(lock.ID(), 2), 0)
	noerr(t, lock.SetRegion(reg))
	wantWords(t, "locked.set_region", s.req(lock.ID(), 2), reg.ID())
	noerr(t, conf.SetRegion(nil))
	wantWords(t, "confined.set_region(nil)", s.req(conf.ID(), 1), 0)

	var seq []string
	lock.OnLocked(func() { seq = append(seq, "locked") })
	lock.OnUnlocked(func() { seq = append(seq, "unlocked") })
	conf.OnConfined(func() { seq = append(seq, "confined") })
	conf.OnUnconfined(func() { seq = append(seq, "unconfined") })
	s.event(lock.ID(), 0)
	s.event(lock.ID(), 1)
	s.event(conf.ID(), 0)
	s.event(conf.ID(), 1)
	if !reflect.DeepEqual(seq, []string{"locked", "unlocked", "confined", "unconfined"}) {
		t.Fatal(seq)
	}
	noerr(t, lock.Destroy())
	s.req(lock.ID(), 0)
	noerr(t, conf.Destroy())
	s.req(conf.ID(), 0)
	noerr(t, pc.Destroy())
	s.req(pc.ID(), 0)
}

// rawPipe returns a pipe as plain descriptors. An *os.File would close a reused
// descriptor number on Close after the binding already closed the original.
func rawPipe(t *testing.T) (*os.File, int) {
	t.Helper()
	var fds [2]int
	noerr(t, unix.Pipe2(fds[:], unix.O_CLOEXEC))
	r := os.NewFile(uintptr(fds[0]), "pipe-r")
	t.Cleanup(func() { r.Close() })
	return r, fds[1]
}

func TestPrimarySelectionNewIDAndFDOwnership(t *testing.T) {
	s := newWireEnv(t)
	seat := s.seat()
	mgr := primaryselection.NewPrimarySelectionDeviceManager(s.d.Context())
	s.bind(1, primaryselection.PrimarySelectionDeviceManagerInterface, 1, 1, mgr)
	dev, e := mgr.GetDevice(seat)
	noerr(t, e)
	wantWords(t, "get_device", s.req(mgr.ID(), 1), dev.ID(), seat.ID())
	src, e := mgr.CreateSource()
	noerr(t, e)
	wantWords(t, "create_source", s.req(mgr.ID(), 0), src.ID())
	noerr(t, src.Offer("text/plain"))
	s.req(src.ID(), 0)
	noerr(t, dev.SetSelection(src, 77))
	wantWords(t, "set_selection", s.req(dev.ID(), 0), src.ID(), 77)
	noerr(t, dev.SetSelection(nil, 78))
	wantWords(t, "set_selection(nil)", s.req(dev.ID(), 0), 0, 78)

	// data_offer carries a server-allocated new_id which must come back as a
	// typed, registered child inheriting the device version.
	var offer *primaryselection.PrimarySelectionOffer
	var mimes []string
	dev.OnDataOffer(func(o *primaryselection.PrimarySelectionOffer) {
		offer = o
		o.OnOffer(func(m string) { mimes = append(mimes, m) })
	})
	var selected []uint32
	dev.OnSelection(func(id uint32) { selected = append(selected, id) })
	s.event(dev.ID(), 0, wordBytes(serverID+1)...)
	if offer == nil || offer.ID() != serverID+1 || offer.Version() != dev.Version() {
		t.Fatalf("offer %+v", offer)
	}
	s.event(offer.ID(), 0, str("text/plain")...)
	s.event(dev.ID(), 1, wordBytes(offer.ID())...)
	s.event(dev.ID(), 1, wordBytes(0)...) // null selection
	if !reflect.DeepEqual(mimes, []string{"text/plain"}) || !reflect.DeepEqual(selected, []uint32{offer.ID(), 0}) {
		t.Fatalf("mimes=%v selected=%v", mimes, selected)
	}

	// Client -> compositor: the sent descriptor is duplicated by the kernel and
	// the client's copy is closed by the binding.
	r, w := rawPipe(t)
	noerr(t, offer.Receive("text/plain", w))
	// Check before reading the request: the received duplicate may reuse the
	// number of the descriptor the client just closed.
	requireFDClosed(t, w)
	body, fd := s.reqFD(offer.ID(), 0)
	// Closed explicitly below; the guard covers early t.Fatal paths only.
	defer func() {
		if fd >= 0 {
			unix.Close(fd)
		}
	}()
	if len(body) < 4 {
		t.Fatalf("receive body %x", body)
	}
	_, e = unix.Write(fd, []byte("payload"))
	noerr(t, e)
	noerr(t, unix.Close(fd))
	fd = -1
	buf := make([]byte, 16)
	n, e := r.Read(buf)
	noerr(t, e)
	if string(buf[:n]) != "payload" {
		t.Fatalf("payload %q", buf[:n])
	}

	// Compositor -> client: source.send hands over an owned descriptor.
	var taken, ignored *wlturbo.OwnedFD
	var sendMimes []string
	src.OnSend(func(m string, f *wlturbo.OwnedFD) {
		sendMimes = append(sendMimes, m)
		if taken == nil {
			taken = f
			n, e := f.Take()
			noerr(t, e)
			_, e = unix.Write(n, []byte("from-client"))
			noerr(t, e)
			unix.Close(n)
			return
		}
		ignored = f // deliberately neither taken nor closed
	})
	sr, sw, e := os.Pipe()
	noerr(t, e)
	send(t, s.p, payload(src.ID(), 0, str("text/plain")...), int(sw.Fd()))
	s.dispatch()
	sw.Close()
	n, e = sr.Read(buf)
	noerr(t, e)
	if string(buf[:n]) != "from-client" {
		t.Fatalf("source payload %q", buf[:n])
	}
	if _, e := taken.Take(); e == nil {
		t.Fatal("second Take on a taken descriptor must fail")
	}
	sr.Close()
	// An unclaimed descriptor is closed by the transport when dispatch ends,
	// so the pipe reader sees EOF although the handler never closed it.
	pr, pw, e := os.Pipe()
	noerr(t, e)
	defer pr.Close()
	send(t, s.p, payload(src.ID(), 0, str("text/html")...), int(pw.Fd()))
	pw.Close() // only the in-flight duplicate keeps the write end alive
	s.dispatch()
	if ignored == nil || len(sendMimes) != 2 {
		t.Fatalf("send events %v", sendMimes)
	}
	if _, e := ignored.Take(); e == nil {
		t.Fatal("unclaimed descriptor must be closed after dispatch, but Take succeeded")
	}
	pr.SetReadDeadline(time.Now().Add(2 * time.Second))
	if n, e := pr.Read(buf); n != 0 || e == nil {
		t.Fatalf("unclaimed descriptor still open: n=%d err=%v", n, e)
	}
	var cancelled bool
	src.OnCancelled(func() { cancelled = true })
	s.event(src.ID(), 1)
	if !cancelled {
		t.Fatal("cancelled not delivered")
	}
	noerr(t, offer.Destroy())
	s.req(offer.ID(), 1)
	noerr(t, src.Destroy())
	s.req(src.ID(), 1)
	noerr(t, dev.Destroy())
	s.req(dev.ID(), 1)
	noerr(t, mgr.Destroy())
	s.req(mgr.ID(), 2)
}

func TestDataControlNewIDAndFDOwnership(t *testing.T) {
	s := newWireEnv(t)
	seat := s.seat()
	mgr := datacontrol.NewExtDataControlManager(s.d.Context())
	s.bind(1, datacontrol.ExtDataControlManagerInterface, 1, 1, mgr)
	dev, e := mgr.GetDataDevice(seat)
	noerr(t, e)
	wantWords(t, "get_data_device", s.req(mgr.ID(), 1), dev.ID(), seat.ID())
	src, e := mgr.CreateDataSource()
	noerr(t, e)
	wantWords(t, "create_data_source", s.req(mgr.ID(), 0), src.ID())
	noerr(t, dev.SetSelection(src))
	wantWords(t, "set_selection", s.req(dev.ID(), 0), src.ID())
	noerr(t, dev.SetPrimarySelection(nil))
	wantWords(t, "set_primary_selection(nil)", s.req(dev.ID(), 2), 0)

	var offers []*datacontrol.ExtDataControlOffer
	dev.OnDataOffer(func(o *datacontrol.ExtDataControlOffer) { offers = append(offers, o) })
	var sel, primary []uint32
	dev.OnSelection(func(id uint32) { sel = append(sel, id) })
	dev.OnPrimarySelection(func(id uint32) { primary = append(primary, id) })
	var finished bool
	dev.OnFinished(func() { finished = true })
	s.event(dev.ID(), 0, wordBytes(serverID+1)...)
	s.event(dev.ID(), 0, wordBytes(serverID+2)...)
	if len(offers) != 2 || offers[0].ID() != serverID+1 || offers[1].ID() != serverID+2 {
		t.Fatalf("offers %+v", offers)
	}
	s.event(dev.ID(), 1, wordBytes(serverID+1)...)
	s.event(dev.ID(), 3, wordBytes(serverID+2)...)
	s.event(dev.ID(), 3, wordBytes(0)...)
	if !reflect.DeepEqual(sel, []uint32{serverID + 1}) || !reflect.DeepEqual(primary, []uint32{serverID + 2, 0}) {
		t.Fatalf("sel=%v primary=%v", sel, primary)
	}
	var mime string
	offers[0].OnOffer(func(m string) { mime = m })
	s.event(offers[0].ID(), 0, str("text/uri-list")...)
	if mime != "text/uri-list" {
		t.Fatal(mime)
	}

	r, w := rawPipe(t)
	noerr(t, offers[0].Receive("text/uri-list", w))
	requireFDClosed(t, w)
	_, fd := s.reqFD(offers[0].ID(), 0)
	unix.Close(fd)
	buf := make([]byte, 8)
	if n, e := r.Read(buf); n != 0 || e == nil {
		t.Fatalf("expected EOF once both write ends closed, n=%d err=%v", n, e)
	}

	// source.send: the handler owns the descriptor and can Close it early.
	var closedByHandler bool
	src.OnSend(func(m string, f *wlturbo.OwnedFD) {
		noerr(t, f.Close())
		noerr(t, f.Close()) // idempotent
		if _, e := f.Take(); e == nil {
			t.Error("Take after Close must fail")
		}
		closedByHandler = true
	})
	pr, pw, e := os.Pipe()
	noerr(t, e)
	defer pr.Close()
	send(t, s.p, payload(src.ID(), 0, str("text/plain")...), int(pw.Fd()))
	pw.Close()
	s.dispatch()
	pr.SetReadDeadline(time.Now().Add(2 * time.Second))
	if n, e := pr.Read(buf); !closedByHandler || n != 0 || e == nil {
		t.Fatalf("closed=%v n=%d err=%v", closedByHandler, n, e)
	}
	s.event(dev.ID(), 2)
	if !finished {
		t.Fatal("finished not delivered")
	}
	noerr(t, src.Offer("text/plain"))
	s.req(src.ID(), 0)
	noerr(t, offers[0].Destroy())
	s.req(offers[0].ID(), 1)
	noerr(t, src.Destroy())
	s.req(src.ID(), 1)
	noerr(t, dev.Destroy())
	s.req(dev.ID(), 1)
	noerr(t, mgr.Destroy())
	s.req(mgr.ID(), 2)
}

func TestWorkspaceLifecycle(t *testing.T) {
	s := newWireEnv(t)
	out := s.output()
	mgr := workspace.NewExtWorkspaceManager(s.d.Context())
	s.bind(1, workspace.ExtWorkspaceManagerInterface, 1, 1, mgr)

	var groups []*workspace.ExtWorkspaceGroupHandle
	var spaces []*workspace.ExtWorkspaceHandle
	var log []string
	mgr.OnWorkspaceGroup(func(g *workspace.ExtWorkspaceGroupHandle) { groups = append(groups, g) })
	mgr.OnWorkspace(func(w *workspace.ExtWorkspaceHandle) { spaces = append(spaces, w) })
	mgr.OnDone(func() { log = append(log, "done") })
	mgr.OnFinished(func() { log = append(log, "finished") })

	s.event(mgr.ID(), 0, wordBytes(serverID+1)...)
	s.event(mgr.ID(), 1, wordBytes(serverID+2)...)
	if len(groups) != 1 || len(spaces) != 1 || groups[0].ID() != serverID+1 || spaces[0].ID() != serverID+2 {
		t.Fatal("typed children missing")
	}
	g, w := groups[0], spaces[0]
	var gcaps, wcaps, state uint32
	var outs, entered []uint32
	var id, name string
	var coords []byte
	g.OnCapabilities(func(c uint32) { gcaps = c })
	g.OnOutputEnter(func(o uint32) { outs = append(outs, o) })
	g.OnWorkspaceEnter(func(x uint32) { entered = append(entered, x) })
	g.OnRemoved(func() { log = append(log, "group-removed") })
	w.OnId(func(v string) { id = v })
	w.OnName(func(v string) { name = v })
	w.OnCoordinates(func(c []byte) { coords = c })
	w.OnState(func(v uint32) { state = v })
	w.OnCapabilities(func(v uint32) { wcaps = v })
	w.OnRemoved(func() { log = append(log, "workspace-removed") })

	s.event(g.ID(), 0, wordBytes(workspace.GROUP_CAPABILITIES_CREATE_WORKSPACE)...)
	s.event(g.ID(), 1, wordBytes(out.ID())...)
	s.event(g.ID(), 3, wordBytes(w.ID())...)
	s.event(w.ID(), 0, str("ws-1")...)
	s.event(w.ID(), 1, str("Workspace é")...)
	s.event(w.ID(), 2, arr(wordBytes(1, 2))...)
	s.event(w.ID(), 3, wordBytes(workspace.STATE_ACTIVE|workspace.STATE_URGENT)...)
	s.event(w.ID(), 4, wordBytes(workspace.WORKSPACE_CAPABILITIES_ACTIVATE|workspace.WORKSPACE_CAPABILITIES_ASSIGN)...)
	s.event(mgr.ID(), 2)
	if gcaps != workspace.GROUP_CAPABILITIES_CREATE_WORKSPACE || !reflect.DeepEqual(outs, []uint32{out.ID()}) || !reflect.DeepEqual(entered, []uint32{w.ID()}) ||
		id != "ws-1" || name != "Workspace é" || !reflect.DeepEqual(decodeWords(coords), []uint32{1, 2}) ||
		state != 3 || wcaps != 9 || !reflect.DeepEqual(log, []string{"done"}) {
		t.Fatalf("state gcaps=%d outs=%v entered=%v id=%q name=%q coords=%v state=%d wcaps=%d log=%v", gcaps, outs, entered, id, name, coords, state, wcaps, log)
	}

	// Requests are only transactional after commit, so the wire order is
	// request, request, commit.
	noerr(t, w.Activate())
	s.req(w.ID(), 1)
	noerr(t, w.Assign(g))
	wantWords(t, "assign", s.req(w.ID(), 3), g.ID())
	noerr(t, g.CreateWorkspace("new"))
	if b := s.req(g.ID(), 0); binary.NativeEndian.Uint32(b) != 4 || string(b[4:7]) != "new" {
		t.Fatalf("create_workspace %x", b)
	}
	noerr(t, w.Deactivate())
	s.req(w.ID(), 2)
	noerr(t, w.Remove())
	s.req(w.ID(), 4)
	noerr(t, mgr.Commit())
	s.req(mgr.ID(), 0)

	// Removal: the compositor sends removed, the client answers with destroy.
	s.event(w.ID(), 5)
	s.event(g.ID(), 5)
	noerr(t, w.Destroy())
	s.req(w.ID(), 0)
	noerr(t, g.Destroy())
	s.req(g.ID(), 1)
	// stop is answered by finished, which is the final event.
	noerr(t, mgr.Stop())
	s.req(mgr.ID(), 1)
	s.event(mgr.ID(), 3)
	if !reflect.DeepEqual(log, []string{"done", "workspace-removed", "group-removed", "finished"}) {
		t.Fatal(log)
	}
	// finished is a destructor event: the manager is dead and must not
	// reach the compositor any more.
	if e := mgr.Commit(); e == nil {
		t.Fatal("Commit after finished must fail")
	}
	s.noRequest()
}

func TestIdleNotifyVersionGate(t *testing.T) {
	s := newWireEnv(t)
	seat := s.seat()
	old := idlenotify.NewExtIdleNotifier(s.d.Context())
	s.bind(1, idlenotify.ExtIdleNotifierInterface, 2, 1, old) // negotiated v1
	if _, e := old.GetInputIdleNotification(1000, seat); !errors.Is(e, wlturbo.ErrVersionTooLow) {
		t.Fatalf("v1 get_input_idle_notification = %v, want ErrVersionTooLow", e)
	}
	s.noRequest() // refused locally, nothing reaches the compositor
	n, e := old.GetIdleNotification(1000, seat)
	noerr(t, e)
	wantWords(t, "get_idle_notification", s.req(old.ID(), 1), n.ID(), 1000, seat.ID())

	send(t, s.p, msg(s.d.Registry().ID(), 1, 1)) // global_remove for the v1 bind
	s.dispatch()
	cur := idlenotify.NewExtIdleNotifier(s.d.Context())
	s.bind(2, idlenotify.ExtIdleNotifierInterface, 2, 2, cur)
	in, e := cur.GetInputIdleNotification(0, seat) // zero timeout is valid
	noerr(t, e)
	wantWords(t, "get_input_idle_notification", s.req(cur.ID(), 2), in.ID(), 0, seat.ID())
	var seq []string
	in.OnIdled(func() { seq = append(seq, "idled") })
	in.OnResumed(func() { seq = append(seq, "resumed") })
	s.event(in.ID(), 0)
	s.event(in.ID(), 1)
	if !reflect.DeepEqual(seq, []string{"idled", "resumed"}) {
		t.Fatal(seq)
	}
	noerr(t, in.Destroy())
	s.req(in.ID(), 0)
	noerr(t, n.Destroy())
	s.req(n.ID(), 0)
}

func TestInhibitAndDesktopHintsWire(t *testing.T) {
	s := newWireEnv(t)
	surf, seat, out := s.surface(), s.seat(), s.output()

	ii := idleinhibit.NewIdleInhibitManager(s.d.Context())
	s.bind(1, idleinhibit.IdleInhibitManagerInterface, 1, 1, ii)
	inh, e := ii.CreateInhibitor(surf)
	noerr(t, e)
	wantWords(t, "create_inhibitor", s.req(ii.ID(), 1), inh.ID(), surf.ID())
	noerr(t, inh.Destroy())
	s.req(inh.ID(), 0)
	noerr(t, ii.Destroy())
	s.req(ii.ID(), 0)

	ks := shortcutsinhibit.NewKeyboardShortcutsInhibitManager(s.d.Context())
	s.bind(2, shortcutsinhibit.KeyboardShortcutsInhibitManagerInterface, 1, 1, ks)
	k, e := ks.InhibitShortcuts(surf, seat)
	noerr(t, e)
	wantWords(t, "inhibit_shortcuts", s.req(ks.ID(), 1), k.ID(), surf.ID(), seat.ID())
	var seq []string
	k.OnActive(func() { seq = append(seq, "active") })
	k.OnInactive(func() { seq = append(seq, "inactive") })
	s.event(k.ID(), 0)
	s.event(k.ID(), 1)
	if !reflect.DeepEqual(seq, []string{"active", "inactive"}) {
		t.Fatal(seq)
	}
	noerr(t, k.Destroy())
	s.req(k.ID(), 0)
	noerr(t, ks.Destroy())
	s.req(ks.ID(), 0)

	xo := xdgoutput.NewZxdgOutputManager(s.d.Context())
	s.bind(3, xdgoutput.ZxdgOutputManagerInterface, 3, 3, xo)
	o, e := xo.GetXdgOutput(out)
	noerr(t, e)
	wantWords(t, "get_xdg_output", s.req(xo.ID(), 1), o.ID(), out.ID())
	var pos, size [2]int32
	var name string
	o.OnLogicalPosition(func(x, y int32) { pos = [2]int32{x, y} })
	o.OnLogicalSize(func(w, h int32) { size = [2]int32{w, h} })
	o.OnName(func(n string) { name = n })
	s.event(o.ID(), 0, wordBytes(uint32(0xfffffff6), 20)...) // -10, 20
	s.event(o.ID(), 1, wordBytes(1920, 1080)...)
	s.event(o.ID(), 3, str("DP-1")...)
	if pos != [2]int32{-10, 20} || size != [2]int32{1920, 1080} || name != "DP-1" {
		t.Fatal(pos, size, name)
	}
	noerr(t, o.Destroy())
	s.req(o.ID(), 0)
	noerr(t, xo.Destroy())
	s.req(xo.ID(), 0)
}

func TestXdgDecorationCrossPackageToplevel(t *testing.T) {
	s := newWireEnv(t)
	surf := s.surface()
	wm := xdgshell.NewXdgWmBase(s.d.Context())
	s.bind(1, xdgshell.XdgWmBaseInterface, 6, 6, wm)
	xs, e := wm.GetXdgSurface(surf)
	noerr(t, e)
	s.req(wm.ID(), 2)
	top, e := xs.GetToplevel()
	noerr(t, e)
	s.req(xs.ID(), 1)

	dm := xdgdecoration.NewZxdgDecorationManager(s.d.Context())
	s.bind(2, xdgdecoration.ZxdgDecorationManagerInterface, 2, 2, dm)
	dec, e := dm.GetToplevelDecoration(top)
	noerr(t, e)
	wantWords(t, "get_toplevel_decoration", s.req(dm.ID(), 1), dec.ID(), top.ID())
	noerr(t, dec.SetMode(xdgdecoration.MODE_SERVER_SIDE))
	wantWords(t, "set_mode", s.req(dec.ID(), 1), xdgdecoration.MODE_SERVER_SIDE)
	var modes []uint32
	dec.OnConfigure(func(m uint32) { modes = append(modes, m) })
	s.event(dec.ID(), 0, wordBytes(xdgdecoration.MODE_CLIENT_SIDE)...)
	if !reflect.DeepEqual(modes, []uint32{xdgdecoration.MODE_CLIENT_SIDE}) {
		t.Fatal(modes)
	}
	noerr(t, dec.UnsetMode())
	s.req(dec.ID(), 2)
	noerr(t, dec.Destroy())
	s.req(dec.ID(), 0)
	noerr(t, dm.Destroy())
	s.req(dm.ID(), 0)
}

func TestImageCaptureCrossPackageChildren(t *testing.T) {
	s := newWireEnv(t)
	out, ptr := s.output(), s.pointer()

	// Foreign toplevel handles come from ext-foreign-toplevel-list and are
	// consumed by a different package (imagecapturesource).
	list := extforeigntoplevel.NewExtForeignToplevelList(s.d.Context())
	s.bind(1, extforeigntoplevel.ExtForeignToplevelListInterface, 1, 1, list)
	var handle *extforeigntoplevel.ExtForeignToplevelHandle
	list.OnToplevel(func(h *extforeigntoplevel.ExtForeignToplevelHandle) { handle = h })
	s.event(list.ID(), 0, wordBytes(serverID+1)...)
	if handle == nil || handle.ID() != serverID+1 {
		t.Fatal("toplevel handle missing")
	}
	var title, appID, ident string
	handle.OnTitle(func(v string) { title = v })
	handle.OnAppId(func(v string) { appID = v })
	handle.OnIdentifier(func(v string) { ident = v })
	s.event(handle.ID(), 2, str("term")...)
	s.event(handle.ID(), 3, str("org.example.term")...)
	s.event(handle.ID(), 4, str("id-1")...)
	if title != "term" || appID != "org.example.term" || ident != "id-1" {
		t.Fatal(title, appID, ident)
	}

	om := imagecapturesource.NewExtOutputImageCaptureSourceManager(s.d.Context())
	s.bind(2, imagecapturesource.ExtOutputImageCaptureSourceManagerInterface, 1, 1, om)
	fm := imagecapturesource.NewExtForeignToplevelImageCaptureSourceManager(s.d.Context())
	s.bind(3, imagecapturesource.ExtForeignToplevelImageCaptureSourceManagerInterface, 1, 1, fm)
	outSrc, e := om.CreateSource(out)
	noerr(t, e)
	wantWords(t, "output create_source", s.req(om.ID(), 0), outSrc.ID(), out.ID())
	topSrc, e := fm.CreateSource(handle)
	noerr(t, e)
	wantWords(t, "toplevel create_source", s.req(fm.ID(), 0), topSrc.ID(), handle.ID())

	cm := imagecopycapture.NewExtImageCopyCaptureManager(s.d.Context())
	s.bind(4, imagecopycapture.ExtImageCopyCaptureManagerInterface, 1, 1, cm)
	sess, e := cm.CreateSession(topSrc, imagecopycapture.OPTIONS_PAINT_CURSORS)
	noerr(t, e)
	wantWords(t, "create_session", s.req(cm.ID(), 0), sess.ID(), topSrc.ID(), imagecopycapture.OPTIONS_PAINT_CURSORS)
	cur, e := cm.CreatePointerCursorSession(outSrc, ptr)
	noerr(t, e)
	wantWords(t, "create_pointer_cursor_session", s.req(cm.ID(), 1), cur.ID(), outSrc.ID(), ptr.ID())

	// Session constraints: advertised before done, arrays decoded.
	var log []string
	var size [2]uint32
	var dev []byte
	var dmaFormat uint32
	var mods []byte
	sess.OnBufferSize(func(w, h uint32) { size = [2]uint32{w, h}; log = append(log, "size") })
	var shm []uint32
	sess.OnShmFormat(func(f uint32) { shm = append(shm, f); log = append(log, "shm") })
	sess.OnDmabufDevice(func(d []byte) { dev = d; log = append(log, "dev") })
	sess.OnDmabufFormat(func(f uint32, m []byte) { dmaFormat, mods = f, m; log = append(log, "dmabuf") })
	sess.OnDone(func() { log = append(log, "done") })
	sess.OnStopped(func() { log = append(log, "stopped") })
	s.event(sess.ID(), 0, wordBytes(1280, 720)...)
	s.event(sess.ID(), 1, wordBytes(1)...) // wl_shm.format xrgb8888
	s.event(sess.ID(), 2, arr(make([]byte, 8))...)
	s.event(sess.ID(), 3, cat(wordBytes(0x34325258), arr(wordBytes(0, 0, 1, 0)))...)
	s.event(sess.ID(), 4)
	if !reflect.DeepEqual(shm, []uint32{1}) {
		t.Fatalf("shm formats %v", shm)
	}
	if size != [2]uint32{1280, 720} || len(dev) != 8 || dmaFormat != 0x34325258 || len(mods) != 16 ||
		!reflect.DeepEqual(log, []string{"size", "shm", "dev", "dmabuf", "done"}) {
		t.Fatal(size, dev, dmaFormat, mods, log)
	}

	frame, e := sess.CreateFrame()
	noerr(t, e)
	wantWords(t, "create_frame", s.req(sess.ID(), 0), frame.ID())
	buf := s.buffer()
	noerr(t, frame.AttachBuffer(buf))
	wantWords(t, "attach_buffer", s.req(frame.ID(), 1), buf.ID())
	noerr(t, frame.DamageBuffer(0, 0, 1280, 720))
	wantWords(t, "damage_buffer", s.req(frame.ID(), 2), 0, 0, 1280, 720)
	noerr(t, frame.Capture())
	s.req(frame.ID(), 3)

	var fl []string
	var damage [4]int32
	var tv [3]uint32
	frame.OnTransform(func(v uint32) { fl = append(fl, "transform") })
	frame.OnDamage(func(x, y, w, h int32) { damage = [4]int32{x, y, w, h}; fl = append(fl, "damage") })
	frame.OnPresentationTime(func(hi, lo, ns uint32) { tv = [3]uint32{hi, lo, ns}; fl = append(fl, "time") })
	frame.OnReady(func() { fl = append(fl, "ready") })
	frame.OnFailed(func(uint32) { fl = append(fl, "failed") })
	s.event(frame.ID(), 0, wordBytes(0)...)
	s.event(frame.ID(), 1, wordBytes(1, 2, 3, 4)...)
	s.event(frame.ID(), 2, wordBytes(0, 1700000000, 999)...)
	s.event(frame.ID(), 3)
	if damage != [4]int32{1, 2, 3, 4} || tv != [3]uint32{0, 1700000000, 999} ||
		!reflect.DeepEqual(fl, []string{"transform", "damage", "time", "ready"}) {
		t.Fatal(damage, tv, fl)
	}
	// ready is terminal for a frame, and a session allows one frame at a time
	// even after ready, so destroy it before creating the frame that fails.
	noerr(t, frame.Destroy())
	s.req(frame.ID(), 0)
	failing, e := sess.CreateFrame()
	noerr(t, e)
	wantWords(t, "create_frame(2)", s.req(sess.ID(), 0), failing.ID())
	noerr(t, failing.AttachBuffer(buf))
	s.req(failing.ID(), 1)
	noerr(t, failing.Capture())
	s.req(failing.ID(), 3)
	var reasons []uint32
	failing.OnReady(func() { t.Error("failed frame must not become ready") })
	failing.OnFailed(func(r uint32) { reasons = append(reasons, r) })
	s.event(failing.ID(), 4, wordBytes(imagecopycapture.FAILURE_REASON_BUFFER_CONSTRAINTS)...)
	if !reflect.DeepEqual(reasons, []uint32{imagecopycapture.FAILURE_REASON_BUFFER_CONSTRAINTS}) {
		t.Fatal(reasons)
	}

	// Cursor session: get_capture_session is a typed child too.
	var cl []string
	var pos, hot [2]int32
	cur.OnEnter(func() { cl = append(cl, "enter") })
	cur.OnLeave(func() { cl = append(cl, "leave") })
	cur.OnPosition(func(x, y int32) { pos = [2]int32{x, y} })
	cur.OnHotspot(func(x, y int32) { hot = [2]int32{x, y} })
	s.event(cur.ID(), 0)
	s.event(cur.ID(), 2, wordBytes(uint32(0xfffffffb), 9)...) // -5, 9
	s.event(cur.ID(), 3, wordBytes(4, 6)...)
	s.event(cur.ID(), 1)
	if pos != [2]int32{-5, 9} || hot != [2]int32{4, 6} || !reflect.DeepEqual(cl, []string{"enter", "leave"}) {
		t.Fatal(pos, hot, cl)
	}
	csess, e := cur.GetCaptureSession()
	noerr(t, e)
	wantWords(t, "get_capture_session", s.req(cur.ID(), 1), csess.ID())
	if csess.Version() != cur.Version() {
		t.Fatalf("child version %d, parent %d", csess.Version(), cur.Version())
	}

	// destroy order: children, sources, managers, toplevel handle.
	for _, c := range []struct {
		name string
		id   uint32
		op   uint16
		fn   func() error
	}{
		{"failed frame", failing.ID(), 0, failing.Destroy},
		{"capture session", csess.ID(), 1, csess.Destroy},
		{"session", sess.ID(), 1, sess.Destroy},
		{"cursor", cur.ID(), 0, cur.Destroy},
		{"output source", outSrc.ID(), 0, outSrc.Destroy},
		{"toplevel source", topSrc.ID(), 0, topSrc.Destroy},
		{"copy manager", cm.ID(), 2, cm.Destroy},
		{"output manager", om.ID(), 1, om.Destroy},
		{"toplevel manager", fm.ID(), 1, fm.Destroy},
		{"handle", handle.ID(), 0, handle.Destroy},
	} {
		// Subtests run serially (no t.Parallel): the wire order of destroy
		// requests on the shared socketpair is part of what is asserted.
		ok := t.Run(c.name, func(t *testing.T) {
			ss := *s
			ss.t = t
			noerr(t, c.fn())
			ss.req(c.id, c.op)
		})
		if !ok {
			// A failed step leaves unread bytes on the socket; carrying on
			// would misattribute them to later destroys.
			t.FailNow()
		}
	}
}
