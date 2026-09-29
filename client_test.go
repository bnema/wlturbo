package wlturbo

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
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

// Close must be safe to call repeatedly and must not report an error from the
// second call.
func TestDisplayCloseIsIdempotent(t *testing.T) {
	d := newDisplay(&chunkConn{})

	if err := d.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if !d.Closed() {
		t.Error("Closed() = false after Close")
	}
}

// A closed display rejects further dispatch instead of reading from a closed
// descriptor.
func TestDisplayDispatchAfterCloseReturnsErrClosed(t *testing.T) {
	d := newDisplay(&chunkConn{chunks: [][]byte{message(7, 3, nil)}})

	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := d.Dispatch(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Dispatch after Close = %v, want %v", err, net.ErrClosed)
	}
}

// Binding registers the proxy for dispatch, and unregistering removes it.
func TestRegistryBindRegistration(t *testing.T) {
	d := newDisplay(&chunkConn{})
	records := []EventRecord{}
	proxy := &recordingProxy{BaseProxy: BaseProxy{id: 7, context: d.Context()}, records: &records}

	if err := d.Registry().Bind(1, "wl_compositor", 4, proxy); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if _, ok := d.objects.Load(uint32(7)); !ok {
		t.Fatal("bound proxy is not registered for dispatch")
	}

	d.Context().Unregister(proxy)
	if _, ok := d.objects.Load(uint32(7)); ok {
		t.Fatal("unregistered proxy is still registered for dispatch")
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
