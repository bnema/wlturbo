package wlturbo

import (
	"bytes"
	"errors"
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
	d.registry.handleGlobal([]byte{1, 0, 0, 0, 4, 0, 0, 0, 'a', 'b', 'c', 0, 1, 0, 0, 0})
	if a != 1 || b != 1 || specific != 1 {
		t.Fatalf("calls a=%d b=%d specific=%d, want 1 each", a, b, specific)
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
