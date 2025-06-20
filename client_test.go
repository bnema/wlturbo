package wlturbo

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// Unit tests that don't require a compositor

func TestFixed(t *testing.T) {
	// Test Fixed type conversion
	tests := []struct {
		input    float64
		expected float64
	}{
		{1.0, 1.0},
		{0.5, 0.5},
		{123.456, 123.456},
		{-1.5, -1.5},
		{0.0, 0.0},
		{256.0, 256.0},
	}

	for _, test := range tests {
		fixed := NewFixed(test.input)
		result := fixed.Float64()

		// Allow small precision differences
		diff := result - test.expected
		if diff < 0 {
			diff = -diff
		}
		if diff > 0.01 {
			t.Errorf("Fixed conversion: input=%f, expected=%f, got=%f",
				test.input, test.expected, result)
		}
	}
}

func TestEventPool(t *testing.T) {
	// Test that the global event pool works without requiring a compositor
	// Get an event
	event := eventPool.Get().(*Event)
	if event == nil {
		t.Fatal("Expected event from pool, got nil")
	}

	// Set some data
	event.ProxyId = 123
	event.Opcode = 456

	// Return to pool
	eventPool.Put(event)

	// Get another event - should be reused
	event2 := eventPool.Get().(*Event)
	if event2 == nil {
		t.Fatal("Expected reused event from pool, got nil")
	}

	// The pool might return a clean event or the same one, both are valid
	t.Logf("Event2: ProxyId=%d, Opcode=%d", event2.ProxyId, event2.Opcode)

	// Clean up
	eventPool.Put(event2)
}

func TestEventDispatcher(t *testing.T) {
	dispatcher := NewEventDispatcher()

	called := false
	handler := func(event *Event) {
		called = true
		if event.ProxyId != 123 || event.Opcode != 1 {
			t.Errorf("Expected ProxyId=123, Opcode=1, got ProxyId=%d, Opcode=%d", event.ProxyId, event.Opcode)
		}
	}

	// Register handler
	dispatcher.RegisterHandler(123, 1, handler)

	// Dispatch event
	dispatcher.Dispatch(123, 1, []byte{})

	if !called {
		t.Error("Handler should have been called")
	}
}

func TestEventDispatcherMultipleHandlers(t *testing.T) {
	dispatcher := NewEventDispatcher()

	called1 := false
	called2 := false

	handler1 := func(event *Event) {
		called1 = true
	}

	handler2 := func(event *Event) {
		called2 = true
	}

	// Register handlers for different opcodes
	dispatcher.RegisterHandler(123, 1, handler1)
	dispatcher.RegisterHandler(123, 2, handler2)

	// Dispatch different events
	dispatcher.Dispatch(123, 1, []byte{})
	dispatcher.Dispatch(123, 2, []byte{})

	if !called1 {
		t.Error("Handler1 should have been called")
	}
	if !called2 {
		t.Error("Handler2 should have been called")
	}
}

func TestMessageMarshalingBasic(t *testing.T) {
	// Create a display to test marshaling (without connecting)
	d := &Display{
		nextID: 2,
	}

	buf := &bytes.Buffer{}

	// Test marshaling different argument types
	tests := []struct {
		name string
		arg  interface{}
		want []byte
	}{
		{
			name: "uint32",
			arg:  uint32(0x12345678),
			want: []byte{0x78, 0x56, 0x34, 0x12}, // little endian
		},
		{
			name: "int32",
			arg:  int32(-1),
			want: []byte{0xFF, 0xFF, 0xFF, 0xFF},
		},
		{
			name: "Fixed",
			arg:  NewFixed(1.0),
			want: []byte{0x00, 0x01, 0x00, 0x00}, // 256 in little endian
		},
		{
			name: "string",
			arg:  "test",
			want: []byte{0x05, 0x00, 0x00, 0x00, 't', 'e', 's', 't', 0x00, 0x00, 0x00, 0x00}, // length + string + null + padding
		},
		{
			name: "nil object",
			arg:  nil,
			want: []byte{0x00, 0x00, 0x00, 0x00},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			buf.Reset()
			err := d.marshalArg(buf, test.arg)
			if err != nil {
				t.Fatalf("marshalArg failed: %v", err)
			}

			got := buf.Bytes()
			if !bytes.Equal(got, test.want) {
				t.Errorf("marshalArg(%v) = %v, want %v", test.arg, got, test.want)
			}
		})
	}
}

func TestMessageHeaderParsing(t *testing.T) {
	// Test parsing message headers
	tests := []struct {
		name     string
		header   []byte
		wantID   uint32
		wantSize uint32
		wantOp   uint16
	}{
		{
			name:     "basic header",
			header:   []byte{0x05, 0x00, 0x00, 0x00, 0x02, 0x00, 0x0C, 0x00}, // ID=5, opcode=2, size=12 (size is upper 16 bits)
			wantID:   5,
			wantSize: 12,
			wantOp:   2,
		},
		{
			name:     "large values",
			header:   []byte{0xFF, 0xFF, 0x00, 0x00, 0xFF, 0x00, 0x00, 0x10}, // ID=65535, opcode=255, size=4096
			wantID:   65535,
			wantSize: 4096,
			wantOp:   255,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if len(test.header) != 8 {
				t.Fatalf("header must be 8 bytes, got %d", len(test.header))
			}

			objectID := binary.LittleEndian.Uint32(test.header[0:4])
			sizeOpcode := binary.LittleEndian.Uint32(test.header[4:8])
			size := sizeOpcode >> 16
			opcode := sizeOpcode & 0xffff

			if objectID != test.wantID {
				t.Errorf("object ID = %d, want %d", objectID, test.wantID)
			}
			if size != test.wantSize {
				t.Errorf("size = %d, want %d", size, test.wantSize)
			}
			if uint16(opcode) != test.wantOp {
				t.Errorf("opcode = %d, want %d", opcode, test.wantOp)
			}
		})
	}
}

func TestAllocateID(t *testing.T) {
	d := &Display{
		nextID: 2, // Start at 2 (1 is reserved for display)
	}

	// Test sequential ID allocation
	id1 := d.allocateID()
	id2 := d.allocateID()
	id3 := d.allocateID()

	if id1 != 2 {
		t.Errorf("First ID = %d, want 2", id1)
	}
	if id2 != 3 {
		t.Errorf("Second ID = %d, want 3", id2)
	}
	if id3 != 4 {
		t.Errorf("Third ID = %d, want 4", id3)
	}
}

func TestRegistryGlobalStorage(t *testing.T) {
	registry := &Registry{
		id:      2,
		globals: make(map[uint32]Global),
	}

	// Test storing and retrieving globals
	global1 := Global{
		Name:      1,
		Interface: "wl_compositor",
		Version:   4,
	}

	global2 := Global{
		Name:      2,
		Interface: "wl_seat",
		Version:   7,
	}

	registry.globals[global1.Name] = global1
	registry.globals[global2.Name] = global2

	// Test GetGlobals
	globals := registry.GetGlobals()
	if len(globals) != 2 {
		t.Errorf("GetGlobals() returned %d globals, want 2", len(globals))
	}

	// Test FindGlobal
	found, exists := registry.FindGlobal("wl_compositor")
	if !exists {
		t.Error("wl_compositor should be found")
	}
	if found.Name != global1.Name || found.Version != global1.Version {
		t.Errorf("Found global = %+v, want %+v", found, global1)
	}

	// Test non-existent global
	_, exists = registry.FindGlobal("non_existent")
	if exists {
		t.Error("non_existent should not be found")
	}
}

func BenchmarkEventDispatch(b *testing.B) {
	dispatcher := NewEventDispatcher()

	handler := func(event *Event) {
		// Minimal handler for benchmarking
	}

	// Register handler
	dispatcher.RegisterHandler(123, 1, handler)

	data := []byte{0x01, 0x02, 0x03, 0x04}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dispatcher.Dispatch(123, 1, data)
	}
}

func BenchmarkFixedConversion(b *testing.B) {
	values := []float64{1.0, 0.5, 123.456, -1.5, 256.789}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, v := range values {
			fixed := NewFixed(v)
			_ = fixed.Float64()
		}
	}
}
