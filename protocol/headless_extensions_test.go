//go:build linux

package protocol_test

import (
	"fmt"
	"sort"
	"testing"

	"github.com/bnema/wlturbo"
	"github.com/bnema/wlturbo/protocol/alphamodifier"
	"github.com/bnema/wlturbo/protocol/colormanagement"
	"github.com/bnema/wlturbo/protocol/colorrepresentation"
	"github.com/bnema/wlturbo/protocol/committiming"
	"github.com/bnema/wlturbo/protocol/contenttype"
	"github.com/bnema/wlturbo/protocol/core"
	"github.com/bnema/wlturbo/protocol/cursorshape"
	"github.com/bnema/wlturbo/protocol/datacontrol"
	"github.com/bnema/wlturbo/protocol/drmlease"
	"github.com/bnema/wlturbo/protocol/drmsyncobj"
	"github.com/bnema/wlturbo/protocol/extforeigntoplevel"
	"github.com/bnema/wlturbo/protocol/fifo"
	"github.com/bnema/wlturbo/protocol/foreigntoplevel"
	"github.com/bnema/wlturbo/protocol/fractionalscale"
	"github.com/bnema/wlturbo/protocol/idleinhibit"
	"github.com/bnema/wlturbo/protocol/idlenotify"
	"github.com/bnema/wlturbo/protocol/imagecapturesource"
	"github.com/bnema/wlturbo/protocol/imagecopycapture"
	"github.com/bnema/wlturbo/protocol/inputmethod"
	"github.com/bnema/wlturbo/protocol/kdeserverdecoration"
	"github.com/bnema/wlturbo/protocol/layershell"
	"github.com/bnema/wlturbo/protocol/linuxdmabuf"
	"github.com/bnema/wlturbo/protocol/outputmanagement"
	"github.com/bnema/wlturbo/protocol/outputpower"
	"github.com/bnema/wlturbo/protocol/pointerconstraints"
	"github.com/bnema/wlturbo/protocol/pointerwarp"
	"github.com/bnema/wlturbo/protocol/presentation"
	"github.com/bnema/wlturbo/protocol/primaryselection"
	"github.com/bnema/wlturbo/protocol/relativepointer"
	"github.com/bnema/wlturbo/protocol/screencopy"
	"github.com/bnema/wlturbo/protocol/shortcutsinhibit"
	"github.com/bnema/wlturbo/protocol/tablet"
	"github.com/bnema/wlturbo/protocol/tearingcontrol"
	"github.com/bnema/wlturbo/protocol/textinput"
	"github.com/bnema/wlturbo/protocol/viewporter"
	"github.com/bnema/wlturbo/protocol/virtualkeyboard"
	"github.com/bnema/wlturbo/protocol/workspace"
	"github.com/bnema/wlturbo/protocol/xdgactivation"
	"github.com/bnema/wlturbo/protocol/xdgdecoration"
	"github.com/bnema/wlturbo/protocol/xdgoutput"
	"github.com/bnema/wlturbo/protocol/xdgshell"
	"golang.org/x/sys/unix"
)

// headlessBinding is one generated root object and the highest version its
// package supports (the XML interface version).
type headlessBinding struct {
	iface     string
	supported uint32
	make      func(*wlturbo.Context) wlturbo.Proxy
	// optional marks globals that depend on GPU, DRM or input devices, or that
	// this NeferWL build does not advertise headless: absence is logged, not
	// a failure. Everything else must be announced.
	optional bool
}

func headlessBindings() []headlessBinding {
	return []headlessBinding{
		{core.CompositorInterface, 7, func(c *wlturbo.Context) wlturbo.Proxy { return core.NewCompositor(c) }, false},
		{core.SubcompositorInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy { return core.NewSubcompositor(c) }, false},
		{core.ShmInterface, 3, func(c *wlturbo.Context) wlturbo.Proxy { return core.NewShm(c) }, false},
		{core.SeatInterface, 11, func(c *wlturbo.Context) wlturbo.Proxy { return core.NewSeat(c) }, false},
		{core.OutputInterface, 4, func(c *wlturbo.Context) wlturbo.Proxy { return core.NewOutput(c) }, false},
		{core.DataDeviceManagerInterface, 4, func(c *wlturbo.Context) wlturbo.Proxy { return core.NewDataDeviceManager(c) }, false},
		{xdgshell.XdgWmBaseInterface, 7, func(c *wlturbo.Context) wlturbo.Proxy { return xdgshell.NewXdgWmBase(c) }, false},
		{linuxdmabuf.LinuxDmabufInterface, 6, func(c *wlturbo.Context) wlturbo.Proxy { return linuxdmabuf.NewLinuxDmabuf(c) }, true},
		{drmsyncobj.WpLinuxDrmSyncobjManagerInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy { return drmsyncobj.NewWpLinuxDrmSyncobjManager(c) }, true},
		{viewporter.WpViewporterInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy { return viewporter.NewWpViewporter(c) }, false},
		{fractionalscale.WpFractionalScaleManagerInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy { return fractionalscale.NewWpFractionalScaleManager(c) }, false},
		{cursorshape.WpCursorShapeManagerInterface, 2, func(c *wlturbo.Context) wlturbo.Proxy { return cursorshape.NewWpCursorShapeManager(c) }, false},
		{tablet.TabletManagerInterface, 2, func(c *wlturbo.Context) wlturbo.Proxy { return tablet.NewTabletManager(c) }, true},
		{textinput.TextInputManagerV3Interface, 2, func(c *wlturbo.Context) wlturbo.Proxy { return textinput.NewTextInputManagerV3(c) }, false},
		{alphamodifier.WpAlphaModifierInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy { return alphamodifier.NewWpAlphaModifier(c) }, false},
		{colormanagement.WpColorManagerInterface, 3, func(c *wlturbo.Context) wlturbo.Proxy { return colormanagement.NewWpColorManager(c) }, false},
		{colorrepresentation.WpColorRepresentationManagerInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy { return colorrepresentation.NewWpColorRepresentationManager(c) }, false},
		{committiming.WpCommitTimingManagerInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy { return committiming.NewWpCommitTimingManager(c) }, false},
		{contenttype.WpContentTypeManagerInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy { return contenttype.NewWpContentTypeManager(c) }, false},
		{datacontrol.ExtDataControlManagerInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy { return datacontrol.NewExtDataControlManager(c) }, false},
		{drmlease.WpDrmLeaseDeviceInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy { return drmlease.NewWpDrmLeaseDevice(c) }, true},
		{extforeigntoplevel.ExtForeignToplevelListInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy { return extforeigntoplevel.NewExtForeignToplevelList(c) }, true},
		{fifo.WpFifoManagerInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy { return fifo.NewWpFifoManager(c) }, false},
		{foreigntoplevel.ForeignToplevelManagerInterface, 3, func(c *wlturbo.Context) wlturbo.Proxy { return foreigntoplevel.NewForeignToplevelManager(c) }, false},
		{idleinhibit.IdleInhibitManagerInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy { return idleinhibit.NewIdleInhibitManager(c) }, false},
		{idlenotify.ExtIdleNotifierInterface, 2, func(c *wlturbo.Context) wlturbo.Proxy { return idlenotify.NewExtIdleNotifier(c) }, false},
		{imagecapturesource.ExtOutputImageCaptureSourceManagerInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy {
			return imagecapturesource.NewExtOutputImageCaptureSourceManager(c)
		}, false},
		{imagecapturesource.ExtForeignToplevelImageCaptureSourceManagerInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy {
			return imagecapturesource.NewExtForeignToplevelImageCaptureSourceManager(c)
		}, true},
		{imagecopycapture.ExtImageCopyCaptureManagerInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy { return imagecopycapture.NewExtImageCopyCaptureManager(c) }, false},
		{inputmethod.InputMethodManagerInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy { return inputmethod.NewInputMethodManager(c) }, false},
		{kdeserverdecoration.OrgKdeKwinServerDecorationManagerInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy {
			return kdeserverdecoration.NewOrgKdeKwinServerDecorationManager(c)
		}, false},
		{layershell.LayerShellInterface, 5, func(c *wlturbo.Context) wlturbo.Proxy { return layershell.NewLayerShell(c) }, false},
		{outputmanagement.OutputManagerInterface, 4, func(c *wlturbo.Context) wlturbo.Proxy { return outputmanagement.NewOutputManager(c) }, false},
		{outputpower.OutputPowerManagerInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy { return outputpower.NewOutputPowerManager(c) }, false},
		{pointerconstraints.PointerConstraintsInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy { return pointerconstraints.NewPointerConstraints(c) }, false},
		{pointerwarp.WpPointerWarpInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy { return pointerwarp.NewWpPointerWarp(c) }, false},
		{presentation.WpPresentationInterface, 2, func(c *wlturbo.Context) wlturbo.Proxy { return presentation.NewWpPresentation(c) }, false},
		{primaryselection.PrimarySelectionDeviceManagerInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy { return primaryselection.NewPrimarySelectionDeviceManager(c) }, false},
		{relativepointer.RelativePointerManagerInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy { return relativepointer.NewRelativePointerManager(c) }, false},
		{screencopy.ScreencopyManagerInterface, 3, func(c *wlturbo.Context) wlturbo.Proxy { return screencopy.NewScreencopyManager(c) }, false},
		{shortcutsinhibit.KeyboardShortcutsInhibitManagerInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy { return shortcutsinhibit.NewKeyboardShortcutsInhibitManager(c) }, false},
		{tearingcontrol.WpTearingControlManagerInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy { return tearingcontrol.NewWpTearingControlManager(c) }, false},
		{virtualkeyboard.VirtualKeyboardManagerInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy { return virtualkeyboard.NewVirtualKeyboardManager(c) }, false},
		{workspace.ExtWorkspaceManagerInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy { return workspace.NewExtWorkspaceManager(c) }, false},
		{xdgactivation.XdgActivationInterface, 1, func(c *wlturbo.Context) wlturbo.Proxy { return xdgactivation.NewXdgActivation(c) }, false},
		{xdgdecoration.ZxdgDecorationManagerInterface, 2, func(c *wlturbo.Context) wlturbo.Proxy { return xdgdecoration.NewZxdgDecorationManager(c) }, false},
		{xdgoutput.ZxdgOutputManagerInterface, 3, func(c *wlturbo.Context) wlturbo.Proxy { return xdgoutput.NewZxdgOutputManager(c) }, false},
	}
}

// waitGlobal blocks in Dispatch until iface is announced. wl_output is added
// by the compositor after startup, so it can follow the first roundtrip. The
// instance context ends a wait that never completes.
func (h *headlessInstance) waitGlobal(t *testing.T, d *wlturbo.Display, iface string) {
	t.Helper()
	for {
		if _, ok := d.Registry().FindGlobal(iface); ok {
			return
		}
		if e := d.Dispatch(); e != nil {
			t.Fatalf("waiting for %s: %v (ctx %v)\n%s", iface, e, h.ctx.Err(), h.Log())
		}
	}
}

// bindOne binds a global at its negotiated version and fails on any error.
func (h *headlessInstance) bindOne(t *testing.T, d *wlturbo.Display, iface string, supported uint32, p wlturbo.Proxy) uint32 {
	t.Helper()
	v, e := d.Registry().BindNegotiated(iface, supported, p)
	if e != nil {
		t.Fatalf("bind %s: %v\n%s", iface, e, h.Log())
	}
	return v
}

func TestHeadlessExtensions(t *testing.T) {
	h := startHeadless(t)

	t.Run("bind announced globals", func(t *testing.T) {
		d := h.connect(t)
		h.waitGlobal(t, d, core.OutputInterface)
		for _, b := range headlessBindings() {
			g, ok := d.Registry().FindGlobal(b.iface)
			if !ok {
				if b.optional {
					t.Logf("%s absent (optional: hardware, device or build dependent)", b.iface)
				} else {
					t.Errorf("%s not announced", b.iface)
				}
				continue
			}
			p := b.make(d.Context())
			v, e := d.Registry().BindNegotiated(b.iface, b.supported, p)
			if e != nil {
				t.Errorf("bind %s: %v", b.iface, e)
				continue
			}
			want := min(g.Version, b.supported)
			if v != want || v == 0 {
				t.Errorf("%s negotiated %d, announced %d supported %d", b.iface, v, g.Version, b.supported)
			}
			if pv, ok := p.(interface{ Version() uint32 }); ok && pv.Version() != v {
				t.Errorf("%s proxy version %d, negotiated %d", b.iface, pv.Version(), v)
			}
			t.Logf("%s announced=%d supported=%d negotiated=%d", b.iface, g.Version, b.supported, v)
		}
		// Every bind request is validated by the compositor before this returns.
		h.sync(t, d, "bind all")
		// Report announced globals that no generated package covers.
		known := map[string]bool{"wl_fixes": true}
		for _, b := range headlessBindings() {
			known[b.iface] = true
		}
		var extra []string
		for _, g := range d.Registry().GetGlobals() {
			if !known[g.Interface] {
				extra = append(extra, g.Interface)
			}
		}
		sort.Strings(extra)
		t.Logf("announced without a client binding: %v", extra)
	})

	t.Run("output description", func(t *testing.T) {
		d := h.connect(t)
		h.waitGlobal(t, d, core.OutputInterface)
		out := core.NewOutput(d.Context())
		h.bindOne(t, d, core.OutputInterface, 4, out)
		xm := xdgoutput.NewZxdgOutputManager(d.Context())
		h.bindOne(t, d, xdgoutput.ZxdgOutputManagerInterface, 3, xm)
		xo, e := xm.GetXdgOutput(out)
		noerr(t, e)
		var lw, lh int32
		var name string
		var wlDone, xdgDone bool
		var modes int
		out.OnMode(func(flags uint32, w, h, refresh int32) { modes++ })
		out.OnDone(func() { wlDone = true })
		xo.OnLogicalSize(func(w, h int32) { lw, lh = w, h })
		xo.OnName(func(n string) { name = n })
		xo.OnDone(func() { xdgDone = true })
		// Registered after the bind request but before any event is read.
		h.sync(t, d, "xdg_output")
		if lw != 640 || lh != 480 || name == "" || modes == 0 || !(wlDone || xdgDone) {
			t.Fatalf("logical=%dx%d name=%q modes=%d wl_done=%t xdg_done=%t\n%s", lw, lh, name, modes, wlDone, xdgDone, h.Log())
		}
		t.Logf("output %q logical %dx%d", name, lw, lh)

		// wlr-output-management lists the same output as a head with modes.
		om := outputmanagement.NewOutputManager(d.Context())
		h.bindOne(t, d, outputmanagement.OutputManagerInterface, 4, om)
		// Head events arrive in any order, so each head keeps its own record.
		type head struct {
			name  string
			modes int
		}
		var heads []*head
		done := false
		om.OnHead(func(hd *outputmanagement.OutputHead) {
			rec := &head{}
			heads = append(heads, rec)
			hd.OnName(func(s string) { rec.name = s })
			hd.OnMode(func(*outputmanagement.OutputMode) { rec.modes++ })
		})
		om.OnDone(func(serial uint32) { done = true })
		h.sync(t, d, "output manager")
		found := false
		for _, rec := range heads {
			found = found || (rec.name == name && rec.modes > 0)
		}
		if !done || !found {
			t.Fatalf("done=%t want head %q with modes; heads:%s\n%s", done, name, func() (s string) {
				for _, rec := range heads {
					s += fmt.Sprintf(" %q/%d", rec.name, rec.modes)
				}
				return
			}(), h.Log())
		}
		noerr(t, xo.Destroy())
		noerr(t, om.Stop())
		h.sync(t, d, "output teardown")
	})

	t.Run("decoration negotiation", func(t *testing.T) {
		d := h.connect(t)
		compositor := core.NewCompositor(d.Context())
		h.bindOne(t, d, core.CompositorInterface, 6, compositor)
		wm := xdgshell.NewXdgWmBase(d.Context())
		h.bindOne(t, d, xdgshell.XdgWmBaseInterface, 6, wm)
		dm := xdgdecoration.NewZxdgDecorationManager(d.Context())
		h.bindOne(t, d, xdgdecoration.ZxdgDecorationManagerInterface, 2, dm)
		kde := kdeserverdecoration.NewOrgKdeKwinServerDecorationManager(d.Context())
		var kdeDefault uint32 = 99
		kde.OnDefaultMode(func(m uint32) { kdeDefault = m })
		h.bindOne(t, d, kdeserverdecoration.OrgKdeKwinServerDecorationManagerInterface, 1, kde)

		surface, e := compositor.CreateSurface()
		noerr(t, e)
		xs, e := wm.GetXdgSurface(surface)
		noerr(t, e)
		top, e := xs.GetToplevel()
		noerr(t, e)
		deco, e := dm.GetToplevelDecoration(top)
		noerr(t, e)
		var modes []uint32
		deco.OnConfigure(func(m uint32) { modes = append(modes, m) })
		// A client asking for client-side is answered with the compositor's server-side mode.
		noerr(t, deco.SetMode(xdgdecoration.MODE_CLIENT_SIDE))
		xs.OnConfigure(func(serial uint32) { noerr(t, xs.AckConfigure(serial)) })
		// The initial empty commit lets the compositor send xdg_surface.configure.
		noerr(t, surface.Commit())
		h.sync(t, d, "decoration")
		if len(modes) == 0 || modes[len(modes)-1] != xdgdecoration.MODE_SERVER_SIDE {
			t.Fatalf("xdg decoration modes %v\n%s", modes, h.Log())
		}
		kdeDeco, e := kde.Create(surface)
		noerr(t, e)
		var kdeMode uint32 = 99
		kdeDeco.OnMode(func(m uint32) { kdeMode = m })
		h.sync(t, d, "kde decoration")
		if kdeDefault != kdeserverdecoration.ORG_KDE_KWIN_SERVER_DECORATION_MANAGER_MODE_SERVER || kdeMode != kdeserverdecoration.ORG_KDE_KWIN_SERVER_DECORATION_MODE_SERVER {
			t.Fatalf("kde default=%d mode=%d", kdeDefault, kdeMode)
		}
		noerr(t, kdeDeco.Release())
		noerr(t, deco.Destroy())
		noerr(t, top.Destroy())
		noerr(t, xs.Destroy())
		noerr(t, surface.Destroy())
		h.sync(t, d, "decoration teardown")
	})

	t.Run("workspaces", func(t *testing.T) {
		d := h.connect(t)
		wsm := workspace.NewExtWorkspaceManager(d.Context())
		groups, workspaces, done, finished := 0, 0, false, false
		names := map[string]bool{}
		wsm.OnWorkspaceGroup(func(*workspace.ExtWorkspaceGroupHandle) { groups++ })
		wsm.OnWorkspace(func(w *workspace.ExtWorkspaceHandle) {
			workspaces++
			w.OnName(func(n string) { names[n] = true })
		})
		wsm.OnDone(func() { done = true })
		wsm.OnFinished(func() { finished = true })
		h.bindOne(t, d, workspace.ExtWorkspaceManagerInterface, 1, wsm)
		h.sync(t, d, "workspace snapshot")
		if !done || groups == 0 || workspaces == 0 || len(names) == 0 {
			t.Fatalf("done=%t groups=%d workspaces=%d names=%v\n%s", done, groups, workspaces, names, h.Log())
		}
		t.Logf("workspaces: groups=%d workspaces=%d names=%v", groups, workspaces, names)
		// stop is answered by finished; wait for it with the blocking dispatch.
		noerr(t, wsm.Stop())
		for !finished {
			if e := d.Dispatch(); e != nil {
				t.Fatalf("waiting for finished: %v\n%s", e, h.Log())
			}
		}
	})

	t.Run("color capabilities", func(t *testing.T) {
		d := h.connect(t)
		cm := colormanagement.NewWpColorManager(d.Context())
		intents, features, tfs, prims := map[uint32]bool{}, map[uint32]bool{}, map[uint32]bool{}, map[uint32]bool{}
		cmDone := false
		cm.OnSupportedIntent(func(v uint32) { intents[v] = true })
		cm.OnSupportedFeature(func(v uint32) { features[v] = true })
		cm.OnSupportedTfNamed(func(v uint32) { tfs[v] = true })
		cm.OnSupportedPrimariesNamed(func(v uint32) { prims[v] = true })
		cm.OnDone(func() { cmDone = true })
		version := h.bindOne(t, d, colormanagement.WpColorManagerInterface, 3, cm)

		cr := colorrepresentation.NewWpColorRepresentationManager(d.Context())
		alpha, crDone := map[uint32]bool{}, false
		cr.OnSupportedAlphaMode(func(v uint32) { alpha[v] = true })
		cr.OnDone(func() { crDone = true })
		h.bindOne(t, d, colorrepresentation.WpColorRepresentationManagerInterface, 1, cr)
		h.sync(t, d, "color capabilities")

		if !cmDone || !intents[colormanagement.RENDER_INTENT_PERCEPTUAL] || !features[colormanagement.FEATURE_PARAMETRIC] ||
			!tfs[colormanagement.TRANSFER_FUNCTION_SRGB] || !prims[colormanagement.PRIMARIES_SRGB] {
			t.Fatalf("color manager v%d done=%t intents=%v features=%v tf=%v primaries=%v\n%s", version, cmDone, intents, features, tfs, prims, h.Log())
		}
		if !crDone || len(alpha) == 0 {
			t.Fatalf("color representation done=%t alpha=%v", crDone, alpha)
		}
		t.Logf("color v%d intents=%v features=%v tf=%v primaries=%v alpha=%v", version, intents, features, tfs, prims, alpha)
	})

	t.Run("presentation clock", func(t *testing.T) {
		d := h.connect(t)
		p := presentation.NewWpPresentation(d.Context())
		clock, got := uint32(0), false
		p.OnClockId(func(id uint32) { clock, got = id, true })
		h.bindOne(t, d, presentation.WpPresentationInterface, 2, p)
		h.sync(t, d, "presentation clock")
		// An empty commit produces no frame, so only the clock is asserted.
		if !got || clock != unix.CLOCK_MONOTONIC {
			t.Fatalf("clock_id got=%t %d, want CLOCK_MONOTONIC", got, clock)
		}
		noerr(t, p.Destroy())
		h.sync(t, d, "presentation teardown")
	})

	t.Run("layer surface configure", func(t *testing.T) {
		d := h.connect(t)
		h.waitGlobal(t, d, core.OutputInterface)
		compositor := core.NewCompositor(d.Context())
		h.bindOne(t, d, core.CompositorInterface, 6, compositor)
		shell := layershell.NewLayerShell(d.Context())
		h.bindOne(t, d, layershell.LayerShellInterface, 5, shell)
		surface, e := compositor.CreateSurface()
		noerr(t, e)
		ls, e := shell.GetLayerSurface(surface, nil, layershell.LAYER_TOP, "wlturbo-test")
		noerr(t, e)
		noerr(t, ls.SetAnchor(layershell.ANCHOR_TOP|layershell.ANCHOR_LEFT|layershell.ANCHOR_RIGHT))
		noerr(t, ls.SetSize(0, 30))
		var serial, w, hgt uint32
		configured := false
		ls.OnConfigure(func(s, cw, ch uint32) { serial, w, hgt, configured = s, cw, ch, true })
		// The initial empty commit asks the compositor for the first configure.
		noerr(t, surface.Commit())
		h.sync(t, d, "layer configure")
		if !configured || w != 640 || hgt != 30 {
			t.Fatalf("configured=%t %dx%d\n%s", configured, w, hgt, h.Log())
		}
		noerr(t, ls.AckConfigure(serial))
		noerr(t, ls.Destroy())
		noerr(t, surface.Destroy())
		h.sync(t, d, "layer teardown")
	})

	t.Run("relative pointer object", func(t *testing.T) {
		d := h.connect(t)
		seat := core.NewSeat(d.Context())
		var caps uint32
		seat.OnCapabilities(func(c uint32) { caps = c })
		h.bindOne(t, d, core.SeatInterface, 8, seat)
		rm := relativepointer.NewRelativePointerManager(d.Context())
		h.bindOne(t, d, relativepointer.RelativePointerManagerInterface, 1, rm)
		h.sync(t, d, "seat capabilities")
		if caps&core.CAPABILITY_POINTER == 0 {
			t.Skipf("seat has no pointer (caps %d); headless input capability dependent", caps)
		}
		ptr, e := seat.GetPointer()
		noerr(t, e)
		rp, e := rm.GetRelativePointer(ptr)
		noerr(t, e)
		// Motion needs pointer focus on a mapped surface, which an empty
		// commit cannot provide; creation and teardown must still be valid.
		rp.OnRelativeMotion(func(uint32, uint32, wlturbo.Fixed, wlturbo.Fixed, wlturbo.Fixed, wlturbo.Fixed) {})
		h.sync(t, d, "relative pointer")
		noerr(t, rp.Destroy())
		noerr(t, ptr.Release())
		noerr(t, rm.Destroy())
		h.sync(t, d, "relative pointer teardown")
	})
}
