//go:build linux

package protocol_test

import (
	"encoding/binary"
	"github.com/bnema/wlturbo"
	"github.com/bnema/wlturbo/protocol/xdgshell"
	"testing"
)

// bindAnnounced selects a supported version using the registry's announcement.
func bindAnnounced(d *wlturbo.Display, iface string, supported uint32, proxy wlturbo.Proxy) (uint32, error) {
	g, ok := d.Registry().FindGlobal(iface)
	if !ok {
		return 0, nil
	}
	version := g.Version
	if version > supported {
		version = supported
	}
	if version == 0 {
		return 0, nil
	}
	return version, d.Registry().Bind(g.Name, iface, version, proxy)
}
func TestAnnouncedVersionNegotiation(t *testing.T) {
	c, p := pair(t)
	d, e := wlturbo.ConnectFromConn(c)
	noerr(t, e)
	defer d.Close()
	request(t, p)
	iface := xdgshell.XdgWmBaseInterface
	if v, e := bindAnnounced(d, iface, 6, xdgshell.NewXdgWmBase(d.Context())); e != nil || v != 0 {
		t.Fatalf("missing: %d %v", v, e)
	}
	for i, version := range []uint32{2, 9} {
		name := uint32(30 + i)
		announcement := append(msg(0, 0, name)[8:], str(iface)...)
		announcement = append(announcement, msg(0, 0, version)[8:]...)
		send(t, p, payload(d.Registry().ID(), 0, announcement...))
		noerr(t, d.Dispatch())
		proxy := xdgshell.NewXdgWmBase(d.Context())
		negotiated, e := bindAnnounced(d, iface, 6, proxy)
		noerr(t, e)
		want := version
		if want > 6 {
			want = 6
		}
		_, op, body := request(t, p)
		if op != 0 || negotiated != want || binary.NativeEndian.Uint32(body) != name || binary.NativeEndian.Uint32(body[len(body)-8:]) != want || binary.NativeEndian.Uint32(body[len(body)-4:]) != proxy.ID() {
			t.Fatalf("server=%d negotiated=%d wire=%x", version, negotiated, body)
		}
		send(t, p, msg(d.Registry().ID(), 1, name))
		noerr(t, d.Dispatch())
	}
}
