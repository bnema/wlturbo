//go:build linux

package protocol_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bnema/wlturbo"
	"github.com/bnema/wlturbo/protocol/core"
	"github.com/bnema/wlturbo/protocol/drmsyncobj"
	"github.com/bnema/wlturbo/protocol/fractionalscale"
	"github.com/bnema/wlturbo/protocol/linuxdmabuf"
	"github.com/bnema/wlturbo/protocol/viewporter"
	"github.com/bnema/wlturbo/protocol/xdgshell"
	"golang.org/x/sys/unix"
)

func TestHeadlessNeferWL(t *testing.T) {
	h := startHeadless(t)
	d := h.connect(t)
	bind := h.binder(t, d)
	ctx := h.ctx
	compositor := core.NewCompositor(d.Context())
	bind(core.CompositorInterface, 6, compositor)
	wm := xdgshell.NewXdgWmBase(d.Context())
	bind(xdgshell.XdgWmBaseInterface, 7, wm)
	dm := linuxdmabuf.NewLinuxDmabuf(d.Context())
	if bind(linuxdmabuf.LinuxDmabufInterface, 4, dm) < 4 {
		t.Fatal("dmabuf feedback requires v4")
	}
	syncManager := drmsyncobj.NewWpLinuxDrmSyncobjManager(d.Context())
	bind(drmsyncobj.WpLinuxDrmSyncobjManagerInterface, 1, syncManager)
	dataManager := core.NewDataDeviceManager(d.Context())
	dataVersion := bind(core.DataDeviceManagerInterface, 4, dataManager)
	vp := viewporter.NewWpViewporter(d.Context())
	bind(viewporter.WpViewporterInterface, 1, vp)
	if g, ok := d.Registry().FindGlobal(fractionalscale.WpFractionalScaleManagerInterface); ok {
		fs := fractionalscale.NewWpFractionalScaleManager(d.Context())
		v := bind(g.Interface, 1, fs)
		if v != 1 {
			t.Fatalf("fractional scale version %d", v)
		}
		noerr(t, fs.Destroy())
	} else {
		t.Log("fractional scale absent")
	}
	surface, e := compositor.CreateSurface()
	noerr(t, e)
	xs, e := wm.GetXdgSurface(surface)
	noerr(t, e)
	top, e := xs.GetToplevel()
	noerr(t, e)
	feedback, e := dm.GetSurfaceFeedback(surface)
	noerr(t, e)
	ss, e := syncManager.GetSurface(surface)
	noerr(t, e)
	configured, done, mainDevice, tableOK := false, false, false, false
	top.OnConfigure(func(w, h int32, states []byte) { t.Logf("xdg_toplevel.configure %dx%d", w, h) })
	xs.OnConfigure(func(serial uint32) {
		noerr(t, xs.AckConfigure(serial))
		configured = true
		t.Logf("xdg_surface.configure ack=%d", serial)
	})
	feedback.OnMainDevice(func(b []byte) { mainDevice = len(b) == 8; t.Logf("main_device=%x", b) })
	feedback.OnFormatTable(func(fd *wlturbo.OwnedFD, size uint32) {
		entries, e := linuxdmabuf.ReadFormatTable(fd, size)
		if e != nil {
			t.Errorf("format table: %v", e)
		} else {
			tableOK = len(entries) > 0
			t.Logf("format-table entries=%d", len(entries))
		}
	})
	feedback.OnDone(func() { done = true })
	// Initial empty commit requests xdg configure; no buffer or window is shown.
	noerr(t, surface.Commit())
	for (!configured || !done) && ctx.Err() == nil {
		if e := d.Dispatch(); e != nil {
			t.Fatalf("dispatch: %v\n%s", e, h.Log())
		}
	}
	if !configured || !done || !mainDevice || !tableOK {
		t.Fatalf("configure=%t feedback=%t main_device=%t table=%t: %v\n%s", configured, done, mainDevice, tableOK, ctx.Err(), h.Log())
	}
	noerr(t, surface.Commit())
	// Drop all child objects while their parents are still alive.
	noerr(t, ss.Destroy())
	noerr(t, feedback.Destroy())
	noerr(t, top.Destroy())
	noerr(t, xs.Destroy())
	noerr(t, surface.Destroy())
	noerr(t, vp.Destroy())
	if dataVersion >= 4 {
		noerr(t, dataManager.Release())
	}
	noerr(t, syncManager.Destroy())
	noerr(t, dm.Destroy())
	noerr(t, wm.Destroy())
	noerr(t, d.Close())
}

// headlessInstance is one isolated NeferWL process with its own runtime,
// config, data and state directories. Cleanup kills the whole process group
// and reaps it. ctx expires at the test deadline or when the process exits, so
// a blocking Dispatch on a connection made by connect always returns.
type headlessInstance struct {
	ctx     context.Context
	socket  string
	logf    *os.File
	done    chan struct{} // closed after the process is reaped
	waitErr error         // valid once done is closed
}

// startHeadless launches WLTURBO_HEADLESS or skips the test when it is unset.
// It returns once the compositor socket exists, or fails with the process log
// if the process exits early or the 18s test deadline expires.
func startHeadless(t *testing.T) *headlessInstance {
	t.Helper()
	binary := os.Getenv("WLTURBO_HEADLESS")
	if binary == "" {
		t.Skip("set WLTURBO_HEADLESS to a NeferWL binary")
	}
	binary, e := filepath.Abs(binary)
	noerr(t, e)
	root := t.TempDir()
	dirs := map[string]string{}
	for _, name := range []string{"run", "config", "data", "state"} {
		dirs[name] = filepath.Join(root, name)
		noerr(t, os.Mkdir(dirs[name], 0700))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
	t.Cleanup(cancel)
	logf, e := os.Create(filepath.Join(root, "neferwl.log"))
	noerr(t, e)
	t.Cleanup(func() { _ = logf.Close() })
	h := &headlessInstance{logf: logf, done: make(chan struct{})}
	// Watch the runtime directory before starting so the socket creation cannot be missed.
	// Non-blocking makes os.File use the poller, so Close unblocks the reader goroutine.
	ifd, e := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	noerr(t, e)
	watch := os.NewFile(uintptr(ifd), "inotify")
	t.Cleanup(func() { _ = watch.Close() })
	_, e = unix.InotifyAddWatch(ifd, dirs["run"], unix.IN_CREATE|unix.IN_ATTRIB)
	noerr(t, e)
	cmd := exec.Command(binary, "--backend=headless", "--no-terminal", "--no-xwayland", "--size", "640x480", "--timeout", "20s")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(cleanDisplayEnv(os.Environ()),
		"XDG_RUNTIME_DIR="+dirs["run"], "XDG_CONFIG_HOME="+dirs["config"],
		"XDG_DATA_HOME="+dirs["data"], "XDG_STATE_HOME="+dirs["state"])
	cmd.Stdout = logf
	cmd.Stderr = logf
	noerr(t, cmd.Start())
	// A failed startup must still terminate the process group and reap it.
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-h.done
	})
	// Process exit cancels ctx, which also unblocks any Dispatch (see connect).
	ctx, stopCtx := context.WithCancel(ctx)
	t.Cleanup(stopCtx)
	h.ctx = ctx
	go func() {
		h.waitErr = cmd.Wait()
		close(h.done)
		stopCtx()
	}()
	events := make(chan struct{}, 1)
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, e := watch.Read(buf); e != nil {
				return
			}
			select {
			case events <- struct{}{}:
			default:
			}
		}
	}()
	find := func() string {
		paths, _ := filepath.Glob(filepath.Join(dirs["run"], "wayland-*"))
		for _, path := range paths {
			if info, e := os.Stat(path); e == nil && info.Mode()&os.ModeSocket != 0 {
				return path
			}
		}
		return ""
	}
	for h.socket = find(); h.socket == ""; h.socket = find() {
		select {
		case <-events:
		case <-h.done:
			t.Fatalf("neferwl exited before serving: %v\n%s", h.waitErr, h.Log())
		case <-ctx.Done():
			t.Fatalf("headless socket timeout: %v\n%s", ctx.Err(), h.Log())
		}
	}
	return h
}

// Log returns the tail of the compositor output.
func (h *headlessInstance) Log() string { return headlessLog(h.logf) }

// connect dials the compositor and completes the initial registry roundtrip.
// The instance context closes the connection when the test deadline expires or
// the compositor exits, so a blocking Dispatch returns instead of hanging.
func (h *headlessInstance) connect(t *testing.T) *wlturbo.Display {
	t.Helper()
	var conn net.Conn
	for {
		var e error
		conn, e = (&net.Dialer{Timeout: time.Second}).DialContext(h.ctx, "unix", h.socket)
		if e == nil {
			break
		}
		// The socket file appears at bind(); listen() follows immediately, so
		// a refused dial is transient unless the compositor is gone.
		if !errors.Is(e, syscall.ECONNREFUSED) {
			t.Fatalf("dial: %v\n%s", e, h.Log())
		}
		select {
		case <-h.done:
			t.Fatalf("neferwl exited before accepting: %v\n%s", h.waitErr, h.Log())
		case <-h.ctx.Done():
			t.Fatalf("dial timeout: %v\n%s", h.ctx.Err(), h.Log())
		case <-time.After(5 * time.Millisecond):
		}
	}
	d, e := wlturbo.ConnectFromConn(conn)
	if e != nil {
		_ = conn.Close()
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = d.Close() })
	stop := context.AfterFunc(h.ctx, func() { _ = d.Close() })
	t.Cleanup(func() { stop() })
	h.sync(t, d, "registry roundtrip")
	return d
}

// binder returns a bind function that fails the test with the compositor log.
func (h *headlessInstance) binder(t *testing.T, d *wlturbo.Display) func(string, uint32, wlturbo.Proxy) uint32 {
	return func(iface string, supported uint32, proxy wlturbo.Proxy) uint32 {
		t.Helper()
		v, err := d.Registry().BindNegotiated(iface, supported, proxy)
		if err != nil {
			t.Fatalf("bind %s: %v\n%s", iface, err, h.Log())
		}
		t.Logf("%s negotiated=%d", iface, v)
		return v
	}
}

// sync completes a roundtrip, reporting compositor protocol errors with its log.
func (h *headlessInstance) sync(t *testing.T, d *wlturbo.Display, what string) {
	t.Helper()
	if e := d.Roundtrip(); e != nil {
		t.Fatalf("%s: %v (ctx %v)\n%s", what, e, h.ctx.Err(), h.Log())
	}
}

// headlessEnvDrop lists the environment prefixes the child must not inherit:
// anything that could attach it to, or make it act on, the caller's session,
// plus NeferWL's own configuration. The test client's own environment is not
// changed; only the child process is isolated.
var headlessEnvDrop = []string{"DISPLAY=", "WAYLAND_DISPLAY=", "WAYLAND_SOCKET=", "NOTIFY_SOCKET=", "DBUS_SESSION_BUS_ADDRESS=",
	"XDG_RUNTIME_DIR=", "XDG_CONFIG_HOME=", "XDG_DATA_HOME=", "XDG_STATE_HOME=", "XDG_SESSION_", "XDG_VTNR=", "XDG_SEAT=",
	"XDG_CURRENT_DESKTOP=", "NIRI_SOCKET=", "SWAYSOCK=", "HYPRLAND_INSTANCE_SIGNATURE=", "NEFERWL_"}

func cleanDisplayEnv(in []string) []string {
	out := make([]string, 0, len(in))
next:
	for _, e := range in {
		for _, p := range headlessEnvDrop {
			if strings.HasPrefix(e, p) {
				continue next
			}
		}
		out = append(out, e)
	}
	return out
}

func TestCleanDisplayEnv(t *testing.T) {
	keep := []string{"PATH=/bin", "HOME=/h", "DISPLAYS=x", "XDG_DATA_DIRS=/d", "MY_NEFERWL_X=1"}
	in := append([]string(nil), keep...)
	for _, p := range headlessEnvDrop {
		e := p + "placeholder"
		if !strings.HasSuffix(p, "=") {
			e += "=1" // a prefix such as XDG_SESSION_ names a family of variables
		}
		in = append(in, e)
	}
	got := cleanDisplayEnv(in)
	if strings.Join(got, "\n") != strings.Join(keep, "\n") {
		t.Fatalf("cleanDisplayEnv = %q, want %q", got, keep)
	}
}

func headlessLog(f *os.File) string {
	b, e := os.ReadFile(f.Name())
	if e != nil {
		return fmt.Sprint(e)
	}
	if len(b) > 8192 {
		b = b[len(b)-8192:]
	}
	return string(b)
}
