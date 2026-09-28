//go:build linux

package protocol_test

import (
	"context"
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
)

func TestHeadlessNeferWL(t *testing.T) {
	binary := os.Getenv("WLTURBO_HEADLESS")
	if binary == "" {
		t.Skip("set WLTURBO_HEADLESS to a NeferWL binary")
	}
	binary, e := filepath.Abs(binary)
	noerr(t, e)
	runtimeDir := filepath.Join(t.TempDir(), "run")
	noerr(t, os.Mkdir(runtimeDir, 0700))
	configDir := filepath.Join(t.TempDir(), "config")
	noerr(t, os.Mkdir(configDir, 0700))
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
	defer cancel()
	cmd := exec.Command(binary, "--backend=headless", "--no-terminal", "--no-xwayland", "--size", "640x480", "--timeout", "20s")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(cleanDisplayEnv(os.Environ()), "XDG_RUNTIME_DIR="+runtimeDir, "XDG_CONFIG_HOME="+configDir)
	log, e := os.Create(filepath.Join(t.TempDir(), "neferwl.log"))
	noerr(t, e)
	defer log.Close()
	cmd.Stdout = log
	cmd.Stderr = log
	noerr(t, cmd.Start())
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Wait() })
	// A failed startup must still terminate its process group and reap it.
	socket := ""
	for socket == "" {
		if e := ctx.Err(); e != nil {
			t.Fatalf("headless socket timeout: %v\n%s", e, headlessLog(log))
		}
		paths, _ := filepath.Glob(filepath.Join(runtimeDir, "wayland-*"))
		for _, path := range paths {
			info, e := os.Stat(path)
			if e == nil && info.Mode()&os.ModeSocket != 0 {
				socket = path
				break
			}
		}
		if socket == "" {
			time.Sleep(20 * time.Millisecond)
		}
	}
	conn, e := net.DialTimeout("unix", socket, time.Second)
	noerr(t, e)
	d, e := wlturbo.ConnectFromConn(conn)
	noerr(t, e)
	defer d.Close()
	// Close the socket when the test deadline expires so blocking Dispatch exits.
	stop := context.AfterFunc(ctx, func() { _ = d.Close() })
	defer stop()
	noerr(t, d.Roundtrip())
	require := func(iface string) wlturbo.Global {
		t.Helper()
		g, ok := d.Registry().FindGlobal(iface)
		if !ok {
			t.Fatalf("required global %s absent\n%s", iface, headlessLog(log))
		}
		t.Logf("%s server=%d", iface, g.Version)
		return g
	}
	bind := func(iface string, supported uint32, proxy wlturbo.Proxy) uint32 {
		t.Helper()
		g := require(iface)
		v := g.Version
		if v > supported {
			v = supported
		}
		noerr(t, d.Registry().Bind(g.Name, iface, v, proxy))
		t.Logf("%s negotiated=%d", iface, v)
		return v
	}
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
	vp := viewporter.NewWpViewporter(d.Context())
	bind(viewporter.WpViewporterInterface, 1, vp)
	if g, ok := d.Registry().FindGlobal(fractionalscale.WpFractionalScaleManagerInterface); ok {
		fs := fractionalscale.NewWpFractionalScaleManager(d.Context())
		v := g.Version
		if v > 1 {
			v = 1
		}
		noerr(t, d.Registry().Bind(g.Name, g.Interface, v, fs))
		t.Logf("%s negotiated=%d", g.Interface, v)
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
	configured, done := false, false
	top.OnConfigure(func(w, h int32, states []byte) { t.Logf("xdg_toplevel.configure %dx%d", w, h) })
	xs.OnConfigure(func(serial uint32) {
		noerr(t, xs.AckConfigure(serial))
		configured = true
		t.Logf("xdg_surface.configure ack=%d", serial)
	})
	feedback.OnMainDevice(func(b []byte) { t.Logf("main_device=%x", b) })
	feedback.OnFormatTable(func(fd *wlturbo.OwnedFD, size uint32) {
		entries, e := linuxdmabuf.ReadFormatTable(fd, size)
		if e != nil {
			t.Errorf("format table: %v", e)
		} else {
			t.Logf("format-table entries=%d", len(entries))
		}
	})
	feedback.OnDone(func() { done = true })
	// Initial empty commit requests xdg configure; no buffer or window is shown.
	noerr(t, surface.Commit())
	for (!configured || !done) && ctx.Err() == nil {
		if e := d.Dispatch(); e != nil {
			t.Fatalf("dispatch: %v\n%s", e, headlessLog(log))
		}
	}
	if !configured || !done {
		t.Fatalf("configure=%t feedback=%t: %v\n%s", configured, done, ctx.Err(), headlessLog(log))
	}
	noerr(t, surface.Commit())
	// Drop all child objects while their parents are still alive.
	noerr(t, ss.Destroy())
	noerr(t, feedback.Destroy())
	noerr(t, top.Destroy())
	noerr(t, xs.Destroy())
	noerr(t, surface.Destroy())
	noerr(t, vp.Destroy())
	noerr(t, syncManager.Destroy())
	noerr(t, dm.Destroy())
	noerr(t, wm.Destroy())
	noerr(t, d.Close())
}
func cleanDisplayEnv(in []string) []string {
	out := make([]string, 0, len(in))
	for _, e := range in {
		if strings.HasPrefix(e, "DISPLAY=") || strings.HasPrefix(e, "WAYLAND_DISPLAY=") || strings.HasPrefix(e, "XDG_RUNTIME_DIR=") || strings.HasPrefix(e, "XDG_CONFIG_HOME=") {
			continue
		}
		out = append(out, e)
	}
	return out
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
