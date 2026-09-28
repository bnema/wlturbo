//go:build linux

package protocol_test

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/bnema/wlturbo"
	"github.com/bnema/wlturbo/protocol/xdgshell"
)

func TestAnnouncedVersionNegotiation(t *testing.T) {
	c, p := pair(t)
	d, e := wlturbo.ConnectFromConn(c)
	noerr(t, e)
	defer d.Close()
	request(t, p)
	iface := xdgshell.XdgWmBaseInterface
	if v, e := d.Registry().BindNegotiated(iface, 6, xdgshell.NewXdgWmBase(d.Context())); v != 0 || !errors.Is(e, wlturbo.ErrGlobalNotFound) {
		t.Fatalf("missing: %d %v", v, e)
	}
	if v, e := d.Registry().BindNegotiated(iface, 0, xdgshell.NewXdgWmBase(d.Context())); v != 0 || e == nil || errors.Is(e, wlturbo.ErrGlobalNotFound) {
		t.Fatalf("unsupported zero: %d %v", v, e)
	}
	for i, version := range []uint32{2, 9} {
		name := uint32(30 + i)
		announcement := append(msg(0, 0, name)[8:], str(iface)...)
		announcement = append(announcement, msg(0, 0, version)[8:]...)
		send(t, p, payload(d.Registry().ID(), 0, announcement...))
		noerr(t, d.Dispatch())
		proxy := xdgshell.NewXdgWmBase(d.Context())
		if v, e := d.Registry().BindNegotiated(iface, 0, proxy); v != 0 || e == nil || errors.Is(e, wlturbo.ErrGlobalNotFound) {
			t.Fatalf("supported zero with global: %d %v", v, e)
		}
		negotiated, e := d.Registry().BindNegotiated(iface, 6, proxy)
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
