package wlturbo

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

// A read deadline must not leave the Display permanently broken.
func TestDispatchDeadlineIsNotSticky(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	d := newDisplay(client)
	records := []EventRecord{}
	p := &recordingProxy{BaseProxy: BaseProxy{id: 7, context: d.context}, records: &records}
	d.objects.Store(uint32(7), p)
	d.RegisterEventSignature(7, 4, "uint,")

	if err := client.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if err := d.Dispatch(); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("Dispatch = %v, want deadline exceeded", err)
	}
	if err := client.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = server.Write(message(7, 4, []byte{1, 0, 0, 0})) }()
	if err := d.Dispatch(); err != nil {
		t.Fatalf("Dispatch after clearing deadline = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
}

type countingGlobalHandler struct{ n *int }

func (h countingGlobalHandler) HandleRegistryGlobal(RegistryGlobalEvent) { *h.n++ }

// Every registered global handler runs, not only the last one.
func TestRegistryGlobalHandlersAccumulate(t *testing.T) {
	d := newDisplay(&chunkConn{})
	var a, b, specific int
	d.registry.AddGlobalHandler(countingGlobalHandler{&a})
	d.registry.AddGlobalHandler(countingGlobalHandler{&b})
	d.registry.AddHandler("abc", func(*Registry, uint32, uint32) { specific++ })
	d.registry.handleGlobal(1, "abc", 1)
	if a != 1 || b != 1 || specific != 1 {
		t.Fatalf("calls a=%d b=%d specific=%d, want 1 each", a, b, specific)
	}
}

type fdSigProxy struct {
	BaseProxy
	calls int
}

func (p *fdSigProxy) EventSignature(op uint16) (string, bool) {
	switch op {
	case 0:
		return "uint,", true
	case 1:
		return "fd,", true
	}
	return "", false
}
func (p *fdSigProxy) Dispatch(*Event) { p.calls++ }

// Events already in flight for an object the client destroyed are dropped,
// with their descriptors closed, until the compositor sends delete_id.
func TestDestroyedObjectEventsAreDroppedUntilDeleteID(t *testing.T) {
	client, peer := unixSocketPair(t)
	d := newDisplay(client)
	p := &fdSigProxy{BaseProxy: BaseProxy{id: d.AllocateID(), context: d.context}}
	d.context.Register(p)
	if err := d.context.SendDestructor(p, 0); err != nil {
		t.Fatal(err)
	}
	if err := d.context.SendRequest(p, 1); err == nil {
		t.Fatal("request on destroyed proxy succeeded")
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	sendMessageWithFDs(t, peer, message(p.ID(), 0, []byte{1, 0, 0, 0}), nil)
	sendMessageWithFDs(t, peer, message(p.ID(), 1, nil), []int{int(w.Fd())})
	w.Close()
	for i := 0; i < 2; i++ {
		if err := d.Dispatch(); err != nil {
			t.Fatalf("in-flight event %d: %v", i, err)
		}
	}
	if p.calls != 0 {
		t.Fatalf("destroyed proxy received %d events", p.calls)
	}
	if err := r.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("descriptor for destroyed object not closed: %v", err)
	}

	body := make([]byte, 4)
	binary.LittleEndian.PutUint32(body, p.ID())
	sendMessageWithFDs(t, peer, message(displayObjectID, 1, body), nil)
	if err := d.Dispatch(); err != nil {
		t.Fatalf("delete_id for zombie: %v", err)
	}
	sendMessageWithFDs(t, peer, message(p.ID(), 0, []byte{1, 0, 0, 0}), nil)
	if err := d.Dispatch(); !errors.Is(err, ErrUnknownObject) {
		t.Fatalf("event after delete_id = %v, want ErrUnknownObject", err)
	}
	if got := d.AllocateID(); got != p.ID() {
		t.Fatalf("ID %d not reused after delete_id (got %d)", p.ID(), got)
	}
}

// A plain Go int is ambiguous (value or descriptor) and must be rejected
// instead of being written as zero.
func TestMarshalRejectsPlainInt(t *testing.T) {
	d := newDisplay(&chunkConn{})
	if err := d.marshalArg(&bytes.Buffer{}, 42); err == nil {
		t.Fatal("marshalArg(int) succeeded, want error")
	}
	if err := d.SendRequest(9, 0, 42); err == nil {
		t.Fatal("SendRequest with int argument succeeded, want error")
	}
}

func TestCheckVersion(t *testing.T) {
	if err := CheckVersion(0, 5, "x.y"); err != nil {
		t.Fatalf("unknown version rejected: %v", err)
	}
	if err := CheckVersion(5, 5, "x.y"); err != nil {
		t.Fatalf("exact version rejected: %v", err)
	}
	if err := CheckVersion(4, 5, "x.y"); !errors.Is(err, ErrVersionTooLow) {
		t.Fatalf("older version = %v, want ErrVersionTooLow", err)
	}
}

// Bind records the negotiated version so generated requests can check it.
func TestBindRecordsVersion(t *testing.T) {
	d := newDisplay(&chunkConn{})
	p := &BaseProxy{}
	if err := d.Registry().Bind(1, "wl_seat", 3, p); err != nil {
		t.Fatal(err)
	}
	if p.Version() != 3 {
		t.Fatalf("version = %d, want 3", p.Version())
	}
}

// A late Unregister of a destroyed proxy (for example from a handler that was
// already running) must not remove its zombie.
func TestUnregisterKeepsZombie(t *testing.T) {
	d := newDisplay(&chunkConn{})
	p := &fdSigProxy{BaseProxy: BaseProxy{id: d.AllocateID(), context: d.context}}
	d.context.Register(p)
	if err := d.context.SendDestructor(p, 0); err != nil {
		t.Fatal(err)
	}
	d.context.Unregister(p)
	if obj, _ := d.objects.Load(p.ID()); obj == nil {
		t.Fatal("Unregister removed the zombie")
	} else if _, ok := obj.(*zombie); !ok {
		t.Fatalf("object = %T, want *zombie", obj)
	}
}

// Context.Request owns the request lifecycle generated code relies on.
func TestContextRequestLifecycle(t *testing.T) {
	d := newDisplay(&chunkConn{})
	parent := &BaseProxy{id: d.AllocateID(), context: d.context, version: 2}
	d.context.Register(parent)

	child := &BaseProxy{context: d.context}
	if err := d.context.Request(Request{Proxy: parent, Opcode: 0, Name: "x.make", Child: child}, child); err != nil {
		t.Fatal(err)
	}
	if child.ID() == 0 || child.Version() != 2 {
		t.Fatalf("child id=%d version=%d, want allocated id and version 2", child.ID(), child.Version())
	}
	if _, ok := d.objects.Load(child.ID()); !ok {
		t.Fatal("child not registered")
	}

	late := &BaseProxy{context: d.context}
	err := d.context.Request(Request{Proxy: parent, Opcode: 1, Name: "x.late", Since: 3, Child: late}, late)
	if !errors.Is(err, ErrVersionTooLow) {
		t.Fatalf("since=3 on v2 = %v, want ErrVersionTooLow", err)
	}
	if late.ID() != 0 {
		t.Fatal("child allocated for a refused request")
	}

	bad := &BaseProxy{context: d.context}
	if err := d.context.Request(Request{Proxy: parent, Opcode: 2, Name: "x.bad", Child: bad}, bad, 42); err == nil {
		t.Fatal("marshal error not reported")
	}
	if _, ok := d.objects.Load(bad.ID()); ok {
		t.Fatal("child left registered after a failed send")
	}
}
