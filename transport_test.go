package wlturbo

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"reflect"
	"testing"
	"time"
)

// EventRecord is what a test observes for one dispatched event.
type EventRecord struct {
	Object uint32
	Opcode uint16
	Body   []byte
}

// recordingProxy records every dispatched event. It is a stand-in for a real
// protocol proxy so framing tests need no compositor.
type recordingProxy struct {
	BaseProxy
	records *[]EventRecord
}

func (p *recordingProxy) Dispatch(event *Event) {
	*p.records = append(*p.records, EventRecord{
		Object: event.ProxyID,
		Opcode: event.Opcode,
		Body:   append([]byte(nil), event.Data()...),
	})
}

// chunkConn returns one chunk per Read, so a test controls exactly how bytes
// are split across read boundaries.
type chunkConn struct {
	chunks [][]byte
	idx    int
}

func (c *chunkConn) Read(p []byte) (int, error) {
	if c.idx >= len(c.chunks) {
		return 0, io.EOF
	}
	chunk := c.chunks[c.idx]
	n := copy(p, chunk)
	switch {
	case n == len(chunk):
		c.idx++
	case n < len(chunk):
		// Caller's buffer was smaller than the chunk: keep the remainder.
		c.chunks[c.idx] = chunk[n:]
	}
	return n, nil
}

func (c *chunkConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *chunkConn) Close() error                     { return nil }
func (c *chunkConn) LocalAddr() net.Addr              { return testAddr{} }
func (c *chunkConn) RemoteAddr() net.Addr             { return testAddr{} }
func (c *chunkConn) SetDeadline(time.Time) error      { return nil }
func (c *chunkConn) SetReadDeadline(time.Time) error  { return nil }
func (c *chunkConn) SetWriteDeadline(time.Time) error { return nil }

type testAddr struct{}

func (testAddr) Network() string { return "test" }
func (testAddr) String() string  { return "test" }

// header builds a Wayland message header for the given object, opcode and
// total message size.
func header(object uint32, opcode uint16, size uint32) []byte {
	buf := make([]byte, HeaderSize)
	binary.LittleEndian.PutUint32(buf[0:4], object)
	binary.LittleEndian.PutUint32(buf[4:8], (size<<16)|uint32(opcode))
	return buf
}

// message builds one complete 4-byte-aligned Wayland message.
func message(object uint32, opcode uint16, body []byte) []byte {
	msg := header(object, opcode, uint32(HeaderSize+len(body)))
	return append(msg, body...)
}

// newChunkDisplay builds a Display over a connection that yields the given
// chunks, with a recording proxy registered at object ID 7.
func newChunkDisplay(chunks [][]byte) (*Display, *[]EventRecord) {
	d := newDisplay(&chunkConn{chunks: chunks})
	records := []EventRecord{}
	d.objects.Store(uint32(7), &recordingProxy{
		BaseProxy: BaseProxy{id: 7, context: d.context},
		records:   &records,
	})
	return d, &records
}

// dispatchUntilError dispatches at most max frames and returns the first error.
func dispatchUntilError(d *Display, max int) error {
	for i := 0; i < max; i++ {
		if err := d.Dispatch(); err != nil {
			return err
		}
	}
	return nil
}

func TestDisplayDispatch_Fragmented(t *testing.T) {
	body := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	msg := message(7, 3, body)

	// Split the header across three reads and the body byte by byte.
	chunks := [][]byte{
		msg[0:3],
		msg[3:5],
		msg[5:HeaderSize],
	}
	for i := HeaderSize; i < len(msg); i++ {
		chunks = append(chunks, msg[i:i+1])
	}

	d, records := newChunkDisplay(chunks)
	err := dispatchUntilError(d, 2)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Dispatch after the message = %v, want %v", err, io.ErrUnexpectedEOF)
	}

	want := []EventRecord{{Object: 7, Opcode: 3, Body: body}}
	if !reflect.DeepEqual(*records, want) {
		t.Fatalf("records = %#v, want %#v", *records, want)
	}
}

func TestDisplayDispatch_Coalesced(t *testing.T) {
	first := message(7, 3, []byte{0x11, 0x12, 0x13, 0x14})
	second := message(7, 4, []byte{0x21, 0x22, 0x23, 0x24})

	d, records := newChunkDisplay([][]byte{append(append([]byte{}, first...), second...)})

	// Two messages arrive in a single read; one Dispatch per message.
	if err := d.Dispatch(); err != nil {
		t.Fatalf("first Dispatch: %v", err)
	}
	if err := d.Dispatch(); err != nil {
		t.Fatalf("second Dispatch: %v", err)
	}

	want := []EventRecord{
		{Object: 7, Opcode: 3, Body: []byte{0x11, 0x12, 0x13, 0x14}},
		{Object: 7, Opcode: 4, Body: []byte{0x21, 0x22, 0x23, 0x24}},
	}
	if !reflect.DeepEqual(*records, want) {
		t.Fatalf("records = %#v, want %#v", *records, want)
	}
}

func TestDisplayDispatch_HalfCoalesced(t *testing.T) {
	// A read boundary that ends between the header and the body of the second
	// message must not be mistaken for a frame boundary.
	whole := append(message(7, 3, []byte{0x01, 0x02, 0x03, 0x04}),
		message(7, 5, []byte{0x05, 0x06, 0x07, 0x08})...)

	split := HeaderSize + 4 + 4 // mid-body of the second message
	d, records := newChunkDisplay([][]byte{whole[:split], whole[split:]})

	if err := d.Dispatch(); err != nil {
		t.Fatalf("first Dispatch: %v", err)
	}
	if err := d.Dispatch(); err != nil {
		t.Fatalf("second Dispatch: %v", err)
	}

	want := []EventRecord{
		{Object: 7, Opcode: 3, Body: []byte{0x01, 0x02, 0x03, 0x04}},
		{Object: 7, Opcode: 5, Body: []byte{0x05, 0x06, 0x07, 0x08}},
	}
	if !reflect.DeepEqual(*records, want) {
		t.Fatalf("records = %#v, want %#v", *records, want)
	}
}

func TestDisplayDispatch_ProtocolError(t *testing.T) {
	cases := []struct {
		name     string
		chunks   [][]byte
		maxLen   uint32
		wantErr  error
		wantKind string
	}{
		{
			name:     "size smaller than header",
			chunks:   [][]byte{header(7, 3, 0)},
			wantErr:  ErrMalformedFrame,
			wantKind: "header_size",
		},
		{
			name:     "misaligned size",
			chunks:   [][]byte{header(7, 3, 10)},
			wantErr:  ErrMalformedFrame,
			wantKind: "misaligned_size",
		},
		{
			name:     "size over configured maximum",
			chunks:   [][]byte{message(7, 3, make([]byte, 32))},
			maxLen:   16,
			wantErr:  ErrMalformedFrame,
			wantKind: "size_over_limit",
		},
		{
			name:     "unknown object",
			chunks:   [][]byte{message(9, 1, []byte{0x01, 0x02, 0x03, 0x04})},
			wantErr:  ErrUnknownObject,
			wantKind: "unknown_object",
		},
		{
			name:     "unknown opcode on display",
			chunks:   [][]byte{message(displayObjectID, 9, nil)},
			wantErr:  ErrUnknownOpcode,
			wantKind: "unknown_opcode",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, records := newChunkDisplay(tc.chunks)
			if tc.maxLen != 0 {
				d.maxMsgLen = tc.maxLen
			}

			err := d.Dispatch()
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Dispatch error = %v, want %v", err, tc.wantErr)
			}

			var perr *ProtocolError
			if !errors.As(err, &perr) {
				t.Fatalf("Dispatch error type = %T, want *ProtocolError", err)
			}
			if perr.Kind != tc.wantKind {
				t.Fatalf("error kind = %q, want %q", perr.Kind, tc.wantKind)
			}
			if len(*records) != 0 {
				t.Fatalf("records = %#v, want none", *records)
			}
		})
	}
}

func TestDisplayDispatch_DisplayError(t *testing.T) {
	// wl_display.error: object id, code, message (padded to 4 bytes).
	msg := []byte("boom")
	body := make([]byte, 0, 8+4+8)
	body = binary.LittleEndian.AppendUint32(body, 7)
	body = binary.LittleEndian.AppendUint32(body, 3)
	body = binary.LittleEndian.AppendUint32(body, uint32(len(msg)+1))
	body = append(body, msg...)
	body = append(body, 0, 0, 0, 0)

	d, _ := newChunkDisplay([][]byte{message(displayObjectID, 0, body)})

	err := d.Dispatch()
	if !errors.Is(err, ErrDisplayError) {
		t.Fatalf("Dispatch error = %v, want %v", err, ErrDisplayError)
	}

	var derr *DisplayError
	if !errors.As(err, &derr) {
		t.Fatalf("Dispatch error type = %T, want *DisplayError", err)
	}
	if derr.ObjectID != 7 || derr.Code != 3 || derr.Message != "boom" {
		t.Fatalf("DisplayError = %+v, want object=7 code=3 message=boom", derr)
	}
}

func TestDisplayDispatch_DeleteID(t *testing.T) {
	d, records := newChunkDisplay([][]byte{message(displayObjectID, 1, []byte{7, 0, 0, 0})})

	if err := d.Dispatch(); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if _, ok := d.objects.Load(uint32(7)); ok {
		t.Fatal("delete_id did not remove object 7")
	}
	if len(*records) != 0 {
		t.Fatalf("records = %#v, want none", *records)
	}
}

func TestDisplayDispatch_PeerClose(t *testing.T) {
	cases := []struct {
		name   string
		chunks [][]byte
	}{
		{name: "closed before any byte"},
		{name: "closed mid header", chunks: [][]byte{{0x01, 0x02, 0x03}}},
		{
			name:   "closed mid body",
			chunks: [][]byte{append(header(7, 3, 16), 0x01, 0x02, 0x03, 0x04)},
		},
		{
			name:   "closed after a complete message",
			chunks: [][]byte{message(7, 3, []byte{0x01, 0x02, 0x03, 0x04})},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := newChunkDisplay(tc.chunks)

			var err error
			for i := 0; i < 4; i++ {
				if err = d.Dispatch(); err != nil {
					break
				}
			}
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("Dispatch error = %v, want %v", err, io.ErrUnexpectedEOF)
			}
		})
	}
}

func TestDisplayDispatch_ClosedReturnsErrClosed(t *testing.T) {
	d, _ := newChunkDisplay([][]byte{message(7, 3, nil)})
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := d.Dispatch(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Dispatch after Close = %v, want %v", err, net.ErrClosed)
	}
}

func TestReadFrame(t *testing.T) {
	body := []byte{0x0a, 0x0b, 0x0c, 0x0d}
	object, opcode, got, err := readFrame(&chunkConn{chunks: [][]byte{message(7, 3, body)}}, DefaultMaxMessageSize)
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if object != 7 || opcode != 3 || !reflect.DeepEqual(got, body) {
		t.Fatalf("readFrame = (%d, %d, %x), want (7, 3, %x)", object, opcode, got, body)
	}

	// A header-only message has an empty body.
	if _, _, got, err := readFrame(&chunkConn{chunks: [][]byte{message(7, 3, nil)}}, 0); err != nil || len(got) != 0 {
		t.Fatalf("readFrame(header only) = (%x, %v), want (empty, nil)", got, err)
	}

	// Truncated bodies are reported, never returned partially.
	if _, _, _, err := readFrame(&chunkConn{chunks: [][]byte{append(header(7, 3, 16), 0x01)}}, 0); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("readFrame(truncated) error = %v, want %v", err, io.ErrUnexpectedEOF)
	}
}
