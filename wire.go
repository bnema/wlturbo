package wlturbo

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	// HeaderSize is the fixed size of a Wayland message header in bytes.
	HeaderSize = 8

	// displayObjectID is the well-known object ID of wl_display.
	displayObjectID = 1

	// DefaultMaxMessageSize bounds a single Wayland message, including its
	// header. Larger messages are rejected instead of allocating.
	DefaultMaxMessageSize = 1 << 20

	// readChunkSize is the size of one read from the display socket.
	readChunkSize = 64 * 1024
)

// Sentinel errors reported by the transport. Wire-level failures wrap one of
// them so callers can classify with errors.Is, while ProtocolError carries the
// offending object, opcode and size.
var (
	// ErrMalformedFrame reports a message whose header violates the wire
	// protocol: too short, misaligned, or larger than the accepted maximum.
	ErrMalformedFrame = errors.New("malformed wayland frame")

	// ErrUnknownObject reports an event for an object this client never
	// created or bound.
	ErrUnknownObject = errors.New("wayland event for unknown object")

	// ErrUnknownOpcode reports an event opcode the target object cannot receive.
	ErrUnknownOpcode = errors.New("wayland event with unknown opcode")

	// ErrDisplayError reports that the compositor sent wl_display.error.
	ErrDisplayError = errors.New("wayland display error")
)

// ProtocolError describes a wire-level protocol violation.
type ProtocolError struct {
	Kind   string
	Object uint32
	Opcode uint16
	Size   uint32
	Err    error
}

func (e *ProtocolError) Error() string {
	msg := e.Kind
	if e.Object != 0 {
		msg += fmt.Sprintf(" object=%d", e.Object)
	}
	if e.Kind == "unknown_opcode" || e.Kind == "display_error" {
		msg += fmt.Sprintf(" opcode=%d", e.Opcode)
	}
	if e.Size != 0 {
		msg += fmt.Sprintf(" size=%d", e.Size)
	}
	if e.Err != nil {
		return fmt.Sprintf("wlturbo: %s: %v", msg, e.Err)
	}
	return "wlturbo: " + msg
}

// Unwrap returns the sentinel error classifying this violation.
func (e *ProtocolError) Unwrap() error { return e.Err }

// DisplayError is returned when the compositor reports wl_display.error.
type DisplayError struct {
	ObjectID uint32
	Code     uint32
	Message  string
}

func (e *DisplayError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("wlturbo: %v: object=%d code=%d", ErrDisplayError, e.ObjectID, e.Code)
	}
	return fmt.Sprintf("wlturbo: %v: object=%d code=%d: %s", ErrDisplayError, e.ObjectID, e.Code, e.Message)
}

// Unwrap reports DisplayError as ErrDisplayError.
func (e *DisplayError) Unwrap() error { return ErrDisplayError }

// receivedFrame is one complete Wayland message read from the connection.
// Ancillary file descriptors are not part of the frame: they are owned by the
// connection and consumed in arrival order by Event.Fd.
type receivedFrame struct {
	object uint32
	opcode uint16
	body   []byte
}

// parseHeader decodes and validates an 8-byte Wayland message header.
func parseHeader(hdr []byte, maxSize uint32) (object uint32, opcode uint16, size uint32, err error) {
	if len(hdr) < HeaderSize {
		return 0, 0, 0, &ProtocolError{Kind: "short_header", Err: ErrMalformedFrame}
	}
	object = binary.LittleEndian.Uint32(hdr[0:4])
	sizeOpcode := binary.LittleEndian.Uint32(hdr[4:8])
	size = sizeOpcode >> 16
	opcode = uint16(sizeOpcode & 0xffff)

	switch {
	case size < HeaderSize:
		return 0, 0, 0, &ProtocolError{Kind: "header_size", Object: object, Opcode: opcode, Size: size, Err: ErrMalformedFrame}
	case size%4 != 0:
		return 0, 0, 0, &ProtocolError{Kind: "misaligned_size", Object: object, Opcode: opcode, Size: size, Err: ErrMalformedFrame}
	case maxSize > 0 && size > maxSize:
		return 0, 0, 0, &ProtocolError{Kind: "size_over_limit", Object: object, Opcode: opcode, Size: size, Err: ErrMalformedFrame}
	}
	return object, opcode, size, nil
}

// readFrame reads exactly one Wayland message from r. A short read never
// yields a partial frame: the header and the declared body are both read in
// full before returning. A maxSize of 0 means DefaultMaxMessageSize.
func readFrame(r io.Reader, maxSize uint32) (object uint32, opcode uint16, body []byte, err error) {
	if maxSize == 0 {
		maxSize = DefaultMaxMessageSize
	}
	var hdr [HeaderSize]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return 0, 0, nil, err
	}
	object, opcode, size, err := parseHeader(hdr[:], maxSize)
	if err != nil {
		return 0, 0, nil, err
	}
	if size == HeaderSize {
		return object, opcode, nil, nil
	}
	body = make([]byte, size-HeaderSize)
	if _, err = io.ReadFull(r, body); err != nil {
		return 0, 0, nil, err
	}
	return object, opcode, body, nil
}

type argKind uint8

const (
	argInvalid argKind = iota // zero Arg: rejected, never marshaled
	argWord                   // uint, int, fixed: one 32-bit word
	argString
	argArray
	argObject
	argFD
)

// Arg is one typed request argument. It carries native values (no interface
// boxing), so building and passing Args to Context.RequestArgs does not
// allocate. The zero Arg is invalid and makes the request fail before any
// write. Build Args with the ArgXxx constructors.
type Arg struct {
	kind argKind
	word uint32
	str  string
	arr  []byte
	obj  Object
}

// ArgUint is a uint argument.
func ArgUint(v uint32) Arg { return Arg{kind: argWord, word: v} }

// ArgInt is an int argument.
func ArgInt(v int32) Arg { return Arg{kind: argWord, word: uint32(v)} }

// ArgFixed is a fixed-point argument.
func ArgFixed(v Fixed) Arg { return Arg{kind: argWord, word: uint32(v)} }

// ArgString is a string argument.
func ArgString(v string) Arg { return Arg{kind: argString, str: v} }

// ArgArray is an array argument.
func ArgArray(v []byte) Arg { return Arg{kind: argArray, arr: v} }

// ArgObject is an object or new_id argument; a nil Object is sent as 0.
func ArgObject(v Object) Arg { return Arg{kind: argObject, obj: v} }

// ArgFD marks a file descriptor position. Descriptors travel out of band in
// Request.FDs, so it contributes no bytes to the message body.
func ArgFD() Arg { return Arg{kind: argFD} }
