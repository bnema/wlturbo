//go:build linux

package wlturbo

import (
	"encoding/binary"
	"errors"
	"io"
	"testing"
	"time"
)

// globalEvent builds a wl_registry.global event body.
func globalEvent(registry, name uint32, iface string, version uint32) []byte {
	padded := (len(iface) + 1 + 3) &^ 3
	body := make([]byte, 4+4+padded+4)
	binary.LittleEndian.PutUint32(body, name)
	binary.LittleEndian.PutUint32(body[4:], uint32(len(iface)+1))
	copy(body[8:], iface)
	binary.LittleEndian.PutUint32(body[8+padded:], version)
	return message(registry, 0, body)
}

func u32Body(v uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	return b
}

// readRequest reads one request the client wrote to the peer.
func readRequest(t *testing.T, r io.Reader) (object uint32, opcode uint16, body []byte) {
	t.Helper()
	object, opcode, body, err := readFrame(r, 0)
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	return object, opcode, body
}

func TestRegistryBootstrapAnnouncements(t *testing.T) {
	client, peer := unixSocketPair(t)
	d := newDisplay(client)
	reg := d.Registry()
	peer.SetDeadline(time.Now().Add(2 * time.Second))

	var got Global
	reg.AddHandler("wl_compositor", func(r *Registry, name, _ uint32) { got, _ = r.FindGlobalByName(name) })

	sendMessageWithFDs(t, peer, globalEvent(reg.ID(), 17, "wl_compositor", 7), nil)
	if err := d.Dispatch(); err != nil {
		t.Fatal(err)
	}
	if got.Name != 17 || got.Interface != "wl_compositor" || got.Version != 7 {
		t.Fatalf("global: %+v", got)
	}

	sendMessageWithFDs(t, peer, message(reg.ID(), 1, u32Body(17)), nil)
	if err := d.Dispatch(); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.FindGlobalByName(17); ok {
		t.Fatal("global not removed")
	}

	sendMessageWithFDs(t, peer, message(reg.ID(), 2, nil), nil)
	if err := d.Dispatch(); !errors.Is(err, ErrUnknownOpcode) {
		t.Fatalf("unknown registry opcode: %v", err)
	}
}

func TestBindNegotiated(t *testing.T) {
	client, peer := unixSocketPair(t)
	d := newDisplay(client)
	reg := d.Registry()
	peer.SetDeadline(time.Now().Add(2 * time.Second))
	const iface = "xdg_wm_base"

	if v, err := reg.BindNegotiated(iface, 6, &BaseProxy{}); v != 0 || !errors.Is(err, ErrGlobalNotFound) {
		t.Fatalf("missing global: %d %v", v, err)
	}
	if v, err := reg.BindNegotiated(iface, 0, &BaseProxy{}); v != 0 || err == nil || errors.Is(err, ErrGlobalNotFound) {
		t.Fatalf("supported zero: %d %v", v, err)
	}

	for i, announced := range []uint32{2, 9} {
		name := uint32(30 + i)
		sendMessageWithFDs(t, peer, globalEvent(reg.ID(), name, iface, announced), nil)
		if err := d.Dispatch(); err != nil {
			t.Fatal(err)
		}
		proxy := &BaseProxy{}
		if v, err := reg.BindNegotiated(iface, 0, proxy); v != 0 || err == nil {
			t.Fatalf("supported zero with global: %d %v", v, err)
		}
		negotiated, err := reg.BindNegotiated(iface, 6, proxy)
		if err != nil {
			t.Fatal(err)
		}
		want := min(announced, 6)
		if negotiated != want || proxy.Version() != want {
			t.Fatalf("announced=%d negotiated=%d proxy version=%d, want %d", announced, negotiated, proxy.Version(), want)
		}
		obj, op, body := readRequest(t, peer)
		// bind: name, string interface, version, new_id.
		if obj != reg.ID() || op != 0 ||
			binary.LittleEndian.Uint32(body) != name ||
			binary.LittleEndian.Uint32(body[len(body)-8:]) != want ||
			binary.LittleEndian.Uint32(body[len(body)-4:]) != proxy.ID() {
			t.Fatalf("bind object=%d opcode=%d body=%x", obj, op, body)
		}
		// Remove the global so the next iteration is the only match.
		sendMessageWithFDs(t, peer, message(reg.ID(), 1, u32Body(name)), nil)
		if err := d.Dispatch(); err != nil {
			t.Fatal(err)
		}
	}
}
