package wlturbo

import (
	"bytes"
	"testing"
)

func TestEventDecoders(t *testing.T) {
	tests := []struct {
		name       string
		data       []byte
		offset     int
		read       func(*Event) any
		want       any
		wantOffset int
	}{
		{"int32 negative", []byte{0xfe, 0xff, 0xff, 0xff}, 0,
			func(e *Event) any { return e.Int32() }, int32(-2), 4},
		{"int32 min", []byte{0, 0, 0, 0x80}, 0,
			func(e *Event) any { return e.Int32() }, int32(-1 << 31), 4},
		{"int32 at offset", []byte{9, 9, 9, 9, 7, 0, 0, 0}, 4,
			func(e *Event) any { return e.Int32() }, int32(7), 8},
		{"int32 truncated", []byte{1, 2, 3}, 0,
			func(e *Event) any { return e.Int32() }, int32(0), 0},
		{"fixed positive", []byte{0x80, 0x01, 0, 0}, 0, // 384/256 = 1.5
			func(e *Event) any { return e.Fixed() }, Fixed(384), 4},
		{"fixed negative", []byte{0x00, 0xff, 0xff, 0xff}, 0, // -256/256 = -1
			func(e *Event) any { return e.Fixed() }, Fixed(-256), 4},
		{"fixed zero", []byte{0, 0, 0, 0}, 0,
			func(e *Event) any { return e.Fixed() }, Fixed(0), 4},
		{"fixed truncated", []byte{1}, 0,
			func(e *Event) any { return e.Fixed() }, Fixed(0), 0},
		{"array aligned", []byte{4, 0, 0, 0, 1, 2, 3, 4, 0xee}, 0,
			func(e *Event) any { return e.Array() }, []byte{1, 2, 3, 4}, 8},
		{"array padded", []byte{5, 0, 0, 0, 1, 2, 3, 4, 5, 0, 0, 0, 0xee}, 0,
			func(e *Event) any { return e.Array() }, []byte{1, 2, 3, 4, 5}, 12},
		{"array zero length", []byte{0, 0, 0, 0, 0xee}, 0,
			func(e *Event) any { return e.Array() }, []byte(nil), 4},
		{"array length past end", []byte{9, 0, 0, 0, 1, 2, 3, 4}, 0,
			func(e *Event) any { return e.Array() }, []byte(nil), 4},
		{"array truncated header", []byte{4, 0, 0}, 0,
			func(e *Event) any { return e.Array() }, []byte(nil), 0},
		{"array at offset", []byte{0xaa, 0xaa, 0xaa, 0xaa, 2, 0, 0, 0, 7, 8, 0, 0}, 4,
			func(e *Event) any { return e.Array() }, []byte{7, 8}, 12},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Event{data: tt.data, offset: tt.offset}
			got := tt.read(e)
			if b, ok := tt.want.([]byte); ok {
				if !bytes.Equal(got.([]byte), b) || (got.([]byte) == nil) != (b == nil) {
					t.Fatalf("got %v, want %v", got, b)
				}
			} else if got != tt.want {
				t.Fatalf("got %v (%T), want %v (%T)", got, got, tt.want, tt.want)
			}
			if e.offset != tt.wantOffset {
				t.Fatalf("offset %d, want %d", e.offset, tt.wantOffset)
			}
		})
	}

	t.Run("array is a copy", func(t *testing.T) {
		data := []byte{4, 0, 0, 0, 1, 2, 3, 4}
		e := &Event{data: data}
		got := e.Array()
		data[4] = 0xff
		if got[0] != 1 {
			t.Fatal("Array aliases the receive buffer")
		}
	})
}
