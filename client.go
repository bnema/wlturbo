// Package wlturbo provides a high-performance Wayland client implementation optimized for gaming and real-time applications.
//
// This package delivers sub-microsecond latency, zero-allocation hot paths, and support for 8000Hz gaming devices.
// It's designed for video game engines, competitive gaming, and other performance-critical applications.
package wlturbo

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

// Pre-allocated buffer pool for performance
var bufferPool = sync.Pool{
	New: func() interface{} {
		return &bytes.Buffer{}
	},
}

// Fixed represents a 24.8 fixed-point number
type Fixed int32

// Float64 converts Fixed to float64
func (f Fixed) Float64() float64 {
	return float64(f) / 256.0
}

// NewFixed creates a Fixed from float64
func NewFixed(v float64) Fixed {
	return Fixed(v * 256.0)
}

// Object represents a Wayland object
type Object interface {
	ID() uint32
}

// Display represents a connection to the Wayland display
type Display struct {
	conn       net.Conn
	unix       *net.UnixConn // set when conn is a Unix socket, nil otherwise
	objects    sync.Map      // map[uint32]Object
	nextID     uint32
	sendMu     sync.Mutex
	recvMu     sync.Mutex
	signatures sync.Map
	listenerMu sync.Mutex
	listeners  sync.Map // map[uint32]map[uint16][]func([]byte)

	// High-performance event dispatcher
	dispatcher *EventDispatcher

	// Core objects
	registry *Registry
	context  *Context // Store context once

	// Error state
	lastError     error
	lastErrorCode uint32
	lastErrorObj  uint32

	// Connection-local receive state. rbuf holds bytes read from the socket
	// that do not yet form a complete frame; recvBuf is the scratch buffer
	// used for a single read. pendingFDs holds ancillary descriptors that
	// arrived with those bytes and have not been consumed yet.
	rbuf       []byte
	recvBuf    []byte
	readErr    error
	maxMsgLen  uint32
	closed     atomic.Bool
	pendingFDs []int
}

// newDisplay builds a Display over an established connection.
func newDisplay(conn net.Conn) *Display {
	d := &Display{
		conn:       conn,
		nextID:     2, // 1 is reserved for wl_display
		dispatcher: NewEventDispatcher(),
	}
	if uc, ok := conn.(*net.UnixConn); ok {
		d.unix = uc
	}
	d.context = NewContext(d)
	// Register display object (ID 1)
	d.objects.Store(uint32(displayObjectID), d)
	// Initialize registry
	d.registry = &Registry{
		id:       d.allocateID(),
		display:  d,
		globals:  make(map[uint32]Global),
		handlers: make(map[string]GlobalHandler),
	}
	d.objects.Store(d.registry.id, d.registry)
	return d
}

// Registry represents the global registry
type Registry struct {
	id             uint32
	display        *Display
	globals        map[uint32]Global
	mu             sync.RWMutex
	handlers       map[string]GlobalHandler
	removeHandlers []RegistryGlobalRemoveHandler
}

// callbackObject represents a wl_callback object
type callbackObject struct {
	BaseProxy
	display *Display
}

func (c *callbackObject) ID() uint32 {
	return c.id
}
func (c *callbackObject) EventSignature(op uint16) (string, bool) {
	if op == 0 {
		return "uint,", true
	}
	return "", false
}

// Dispatch handles callback events (opcode 0 = done)
func (c *callbackObject) Dispatch(event *Event) {
	if event.Opcode != 0 { // done event
		return
	}
	c.display.notifyListeners(c.id, 0, event.data)
	c.display.context.Unregister(c)
}

// Global represents a global object
type Global struct {
	Name      uint32
	Interface string
	Version   uint32
}

// GlobalHandler is called when a global is announced
type GlobalHandler func(registry *Registry, name uint32, version uint32)

// ConnectFromConn attaches to an established Wayland socket and requests its registry.
func ConnectFromConn(conn net.Conn) (*Display, error) {
	d := newDisplay(conn)
	if err := d.getRegistry(); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("get registry: %w", err)
	}
	return d, nil
}

// Connect connects to the Wayland display and requests the registry.
func Connect(socketPath string) (*Display, error) {
	if socketPath == "" {
		socketPath = os.Getenv("WAYLAND_DISPLAY")
		if socketPath == "" {
			socketPath = "wayland-0"
		}
	}

	// Resolve socket path
	if !filepath.IsAbs(socketPath) {
		runDir := os.Getenv("XDG_RUNTIME_DIR")
		if runDir == "" {
			return nil, errors.New("XDG_RUNTIME_DIR not set")
		}
		socketPath = filepath.Join(runDir, socketPath)
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Wayland: %w", err)
	}

	return ConnectFromConn(conn)
}

// Close closes the display connection. It is safe to call more than once;
// later calls are no-ops. After Close, Dispatch returns net.ErrClosed.
func (d *Display) Close() error {
	if !d.closed.CompareAndSwap(false, true) {
		return nil
	}
	d.context.closed.Store(true)
	err := d.conn.Close()
	d.sendMu.Lock()
	d.sendMu.Unlock()
	d.recvMu.Lock()
	d.closePendingFDs()
	d.recvMu.Unlock()
	return err
}

// Closed reports whether the connection has been closed.
func (d *Display) Closed() bool {
	return d.closed.Load()
}

// ID returns the display's object ID (always 1)
func (d *Display) ID() uint32 {
	return 1
}

// RegisterEventHandler registers a high-performance event handler
func (d *Display) RegisterEventHandler(objectID uint32, opcode uint16, handler EventHandler) {
	d.dispatcher.RegisterHandler(objectID, opcode, handler)
}

// allocateID allocates a new object ID
func (d *Display) allocateID() uint32 {
	return atomic.AddUint32(&d.nextID, 1) - 1
}

// AllocateID allocates a new object ID (public method)
func (d *Display) AllocateID() uint32 {
	return d.allocateID()
}

// SendRequest sends a request to the compositor
func (d *Display) SendRequest(objectID uint32, opcode uint16, args ...interface{}) error {
	return d.SendRequestWithFDs(objectID, opcode, nil, args...)
}

// SendRequestWithFDs sends a request with file descriptors
func (d *Display) SendRequestWithFDs(objectID uint32, opcode uint16, fds []int, args ...interface{}) error {
	// Get buffer from pool
	buf := bufferPool.Get().(*bytes.Buffer)
	defer func() {
		buf.Reset()
		bufferPool.Put(buf)
	}()

	// Write header placeholder
	header := make([]byte, 8)
	_, _ = buf.Write(header)

	// Marshal arguments
	for _, arg := range args {
		if err := d.marshalArg(buf, arg); err != nil {
			return fmt.Errorf("failed to marshal argument: %w", err)
		}
	}

	// Update header with actual size in bytes
	bufLen := buf.Len()
	if bufLen > 0xFFFF || bufLen%4 != 0 {
		return fmt.Errorf("invalid message size: %d bytes", bufLen)
	}
	size := uint32(bufLen) // Safe: checked above
	binary.LittleEndian.PutUint32(header[0:4], objectID)
	// Upper 16 bits = size, lower 16 bits = opcode
	binary.LittleEndian.PutUint32(header[4:8], (size<<16)|uint32(opcode))

	// Update buffer with correct header
	data := buf.Bytes()
	copy(data[0:8], header)

	// Send message with optional file descriptors
	return d.sendmsgWithFDs(data, fds)
}

// marshalArg marshals a single argument
func (d *Display) marshalArg(buf *bytes.Buffer, arg interface{}) error {
	switch v := arg.(type) {
	case uint32:
		return binary.Write(buf, binary.LittleEndian, v)
	case int32:
		return binary.Write(buf, binary.LittleEndian, v)
	case Fixed:
		return binary.Write(buf, binary.LittleEndian, int32(v))
	case string:
		// String format: length (including null) + string + null + padding
		strlen := len(v) + 1
		if strlen > 0xffff-HeaderSize {
			return fmt.Errorf("string too long: %d bytes", strlen)
		}
		if err := binary.Write(buf, binary.LittleEndian, uint32(strlen)); err != nil { // Safe: checked above
			return err
		}
		_, _ = buf.WriteString(v)
		_ = buf.WriteByte(0)
		// Pad to 32-bit boundary
		padding := (4 - (strlen % 4)) % 4
		for i := 0; i < padding; i++ {
			_ = buf.WriteByte(0)
		}
	case []byte:
		// Array format: length + data + padding
		arrlen := len(v)
		if arrlen > 0xffff-HeaderSize {
			return fmt.Errorf("array too long: %d bytes", arrlen)
		}
		if err := binary.Write(buf, binary.LittleEndian, uint32(arrlen)); err != nil {
			return err
		}
		_, _ = buf.Write(v)
		// Pad to 32-bit boundary
		padding := (4 - (arrlen % 4)) % 4
		for i := 0; i < padding; i++ {
			_ = buf.WriteByte(0)
		}
	case Object:
		if v != nil {
			return binary.Write(buf, binary.LittleEndian, v.ID())
		}
		return binary.Write(buf, binary.LittleEndian, uint32(0))
	case nil:
		// Null object
		return binary.Write(buf, binary.LittleEndian, uint32(0))
	case int:
		// File descriptor - write placeholder in message
		// Actual FD will be sent via SCM_RIGHTS
		return binary.Write(buf, binary.LittleEndian, uint32(0))
	case uintptr:
		// File descriptor as uintptr - NO placeholder in message for neurlang compatibility
		// FD is ONLY sent via SCM_RIGHTS, not in the message body
		return nil
	default:
		return fmt.Errorf("unsupported argument type: %T", arg)
	}
	return nil
}

// Dispatch reads one complete Wayland message and delivers it to the object
// that owns it.
//
// Read boundaries never define message boundaries: a header split across
// several reads and several messages arriving in one read are both handled.
// Dispatch returns net.ErrClosed after Close, a typed ProtocolError for a
// malformed frame, and io.ErrUnexpectedEOF when the peer closes mid-frame.
func (d *Display) Dispatch() error {
	d.recvMu.Lock()
	defer d.recvMu.Unlock()

	if d.closed.Load() {
		return net.ErrClosed
	}

	frame, err := d.nextFrame()
	if d.closed.Load() {
		return net.ErrClosed
	}
	if err != nil {
		return d.fail(err)
	}
	return d.fail(d.dispatchFrame(frame))
}

// fail releases resources that cannot be used once the connection is
// unusable. A protocol violation invalidates the whole connection, so queued
// descriptors are closed; transport errors may be transient and keep them.
func (d *Display) fail(err error) error {
	if err == nil {
		return nil
	}
	var perr *ProtocolError
	if errors.As(err, &perr) {
		d.closePendingFDs()
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		d.closePendingFDs()
	}
	return err
}

// maxMessageSize reports the largest accepted message size.
func (d *Display) maxMessageSize() uint32 {
	if d.maxMsgLen == 0 {
		return DefaultMaxMessageSize
	}
	return d.maxMsgLen
}

// nextFrame returns the next complete frame, reading more bytes as needed.
func (d *Display) nextFrame() (receivedFrame, error) {
	for {
		if len(d.rbuf) >= HeaderSize {
			object, opcode, size, err := parseHeader(d.rbuf[:HeaderSize], d.maxMessageSize())
			if err != nil {
				return receivedFrame{}, err
			}
			if uint32(len(d.rbuf)) >= size {
				frame := receivedFrame{object: object, opcode: opcode}
				if size > HeaderSize {
					frame.body = make([]byte, size-HeaderSize)
					copy(frame.body, d.rbuf[HeaderSize:size])
				}
				d.consume(size)
				return frame, nil
			}
		}
		if err := d.fill(); err != nil {
			if d.closed.Load() {
				return receivedFrame{}, net.ErrClosed
			}
			return receivedFrame{}, err
		}
	}
}

// consume removes the first n bytes from the receive buffer.
func (d *Display) consume(n uint32) {
	rest := copy(d.rbuf, d.rbuf[n:])
	d.rbuf = d.rbuf[:rest]
}

// fill reads one chunk from the connection into the receive buffer. Bytes that
// arrive together with an error are kept so they are still parsed before the
// error surfaces.
func (d *Display) fill() error {
	if d.readErr != nil {
		return d.readErr
	}
	if d.recvBuf == nil {
		d.recvBuf = make([]byte, readChunkSize)
	}
	n, err := d.readChunk()
	if n > 0 {
		d.rbuf = append(d.rbuf, d.recvBuf[:n]...)
	}
	if err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		d.readErr = err
		// A control-message failure corrupts the descriptor stream, so it is
		// reported immediately instead of after the buffered bytes are used.
		var perr *ProtocolError
		if n == 0 || errors.As(err, &perr) {
			return err
		}
	}
	return nil
}

// signatureProvider is implemented by generated objects; every opcode must
// have a known signature before any descriptor can be assigned or dispatched.
type signatureProvider interface{ EventSignature(uint16) (string, bool) }

// dispatchFrame delivers one complete frame with precisely its signature's FDs.
func (d *Display) dispatchFrame(f receivedFrame) error {
	if f.object == displayObjectID {
		if len(d.pendingFDs) != 0 && len(d.rbuf) == 0 {
			return &ProtocolError{Kind: "extra_fd", Err: ErrMalformedFrame}
		}
		return d.handleDisplayEvent(f.opcode, f.body)
	}
	obj, ok := d.objects.Load(f.object)
	if !ok {
		return &ProtocolError{Kind: "unknown_object", Object: f.object, Opcode: f.opcode, Err: ErrUnknownObject}
	}
	sig, valid := "", false
	p, isGenerated := obj.(signatureProvider)
	if isGenerated {
		sig, valid = p.EventSignature(f.opcode)
	}
	// Legacy custom proxies may declare their signatures with RegisterEventSignature.
	if !valid && !isGenerated {
		if v, ok := d.signatures.Load(signatureKey{f.object, f.opcode}); ok {
			sig, valid = v.(string), true
		}
	}
	if !valid {
		return &ProtocolError{Kind: "unknown_opcode", Object: f.object, Opcode: f.opcode, Err: ErrUnknownOpcode}
	}
	if err := validateEventBody(sig, f.body); err != nil {
		return &ProtocolError{Kind: "malformed_payload", Object: f.object, Opcode: f.opcode, Err: err}
	}
	count := strings.Count(sig, "fd,")
	if len(d.pendingFDs) < count {
		return &ProtocolError{Kind: "missing_fd", Object: f.object, Opcode: f.opcode, Err: ErrMalformedFrame}
	}
	ev := eventPool.Get().(*Event)
	*ev = Event{ProxyID: f.object, Opcode: f.opcode, data: f.body, display: d}
	if count != 0 {
		ev.fds = make([]*OwnedFD, count)
		for i := range ev.fds {
			ev.fds[i] = &OwnedFD{fd: d.pendingFDs[i]}
		}
		d.pendingFDs = d.pendingFDs[count:]
	}
	defer func() {
		for _, fd := range ev.fds {
			_ = fd.Close()
		}
		*ev = Event{}
		eventPool.Put(ev)
	}()
	// FDs from a single recvmsg can accompany multiple coalesced frames. Once
	// all buffered bytes have been framed, surplus descriptors are not legal.
	if len(d.rbuf) == 0 && len(d.pendingFDs) != 0 {
		return &ProtocolError{Kind: "extra_fd", Object: f.object, Opcode: f.opcode, Err: ErrMalformedFrame}
	}
	if proxy, ok := obj.(Proxy); ok {
		proxy.Dispatch(ev)
	} else {
		d.dispatcher.Dispatch(f.object, f.opcode, f.body)
		d.notifyListeners(f.object, f.opcode, f.body)
	}
	return nil
}

type signatureKey struct {
	object uint32
	opcode uint16
}

// RegisterEventSignature defines a signature for a legacy proxy. Generated
// proxies supply it themselves and cannot be overridden.
func (d *Display) RegisterEventSignature(object uint32, opcode uint16, signature string) {
	if object == 0 || object == displayObjectID {
		return
	}
	d.signatures.Store(signatureKey{object, opcode}, signature)
}

// notifyListeners runs the listeners registered for an object and opcode.
func (d *Display) notifyListeners(objectID uint32, opcode uint16, body []byte) {
	listeners, ok := d.listeners.Load(objectID)
	if !ok {
		return
	}
	opcodeMap, ok := listeners.(*sync.Map)
	if !ok {
		return
	}
	handlers, ok := opcodeMap.Load(opcode)
	if !ok {
		return
	}
	for _, handler := range handlers.([]func([]byte)) {
		if handler != nil {
			handler(body)
		}
	}
}

// handleDisplayEvent handles events on the display object
func (d *Display) handleDisplayEvent(opcode uint16, data []byte) error {
	switch opcode {
	case 0: // error
		if err := validateEventBody("object,uint,string,", data); err != nil {
			return &ProtocolError{Kind: "malformed_display_error", Err: err}
		}
		event := &Event{ProxyID: displayObjectID, Opcode: opcode, data: data, display: d}
		objectID := event.Uint32()
		code := event.Uint32()
		message := event.String()

		d.lastErrorCode = code
		d.lastErrorObj = objectID
		d.lastError = &DisplayError{ObjectID: objectID, Code: code, Message: message}
		return d.lastError

	case 1: // delete_id
		if len(data) != 4 {
			return &ProtocolError{Kind: "short_delete_id", Object: displayObjectID, Opcode: opcode, Size: uint32(len(data)) + HeaderSize, Err: ErrMalformedFrame}
		}
		id := binary.LittleEndian.Uint32(data[0:4])
		d.objects.Delete(id)
		d.context.proxies.Delete(id)
		return nil

	default:
		return &ProtocolError{Kind: "unknown_opcode", Object: displayObjectID, Opcode: opcode, Err: ErrUnknownOpcode}
	}
}

// Roundtrip waits for one wl_display.sync callback. Concurrent event loops
// must not call Roundtrip while another goroutine dispatches this connection.
func (d *Display) Roundtrip() error {
	callback, err := d.Sync()
	if err != nil {
		return err
	}
	for {
		if err = d.Dispatch(); err != nil {
			return err
		}
		if _, exists := d.objects.Load(callback.ID()); !exists {
			return nil
		}
	}
}

// AddListener adds an event listener for an object
func (d *Display) AddListener(objectID uint32, opcode uint16, handler func([]byte)) {
	if handler == nil {
		return
	}
	// Load or create listener map for object
	listeners, _ := d.listeners.LoadOrStore(objectID, &sync.Map{})
	opcodeMap := listeners.(*sync.Map)

	d.listenerMu.Lock()
	defer d.listenerMu.Unlock()
	var handlers []func([]byte)
	if old, ok := opcodeMap.Load(opcode); ok {
		handlers = old.([]func([]byte))
	}
	next := make([]func([]byte), len(handlers)+1)
	copy(next, handlers)
	next[len(handlers)] = handler
	opcodeMap.Store(opcode, next)
}

// getRegistry gets the global registry
func (d *Display) getRegistry() error {
	// Add registry listeners
	d.AddListener(d.registry.id, 0, d.registry.handleGlobal)
	d.AddListener(d.registry.id, 1, d.registry.handleGlobalRemove)

	// Send get_registry request (opcode 1)
	return d.SendRequest(1, 1, d.registry.id)
}

// Registry returns the global registry
func (d *Display) Registry() *Registry {
	return d.registry
}

// ID returns the registry's object ID
func (r *Registry) ID() uint32 {
	return r.id
}

// handleGlobal handles global announcements
func (r *Registry) handleGlobal(data []byte) {
	if len(data) < 8 {
		return
	}

	name := binary.LittleEndian.Uint32(data[0:4])
	ifaceLen := binary.LittleEndian.Uint32(data[4:8])

	if ifaceLen == 0 || uint64(ifaceLen)+12 > uint64(len(data)) || data[8+ifaceLen-1] != 0 {
		return
	}

	// String includes null terminator in length
	iface := string(data[8 : 8+ifaceLen-1]) // -1 to remove null terminator

	// Calculate padding for 32-bit alignment
	padding := (4 - (ifaceLen % 4)) % 4
	versionOffset := 8 + int(ifaceLen) + int(padding)

	if len(data) != versionOffset+4 {
		return
	}

	version := binary.LittleEndian.Uint32(data[versionOffset:])

	// Store global
	r.mu.Lock()
	r.globals[name] = Global{
		Name:      name,
		Interface: iface,
		Version:   version,
	}
	r.mu.Unlock()

	// Call specific handler if registered
	r.mu.RLock()
	handler, specific := r.handlers[iface]
	wildcard, all := r.handlers["*"]
	r.mu.RUnlock()
	if specific {

		handler(r, name, version)
	}

	// Call wildcard handler if registered
	if all {

		wildcard(r, name, version)
	}
}

// handleGlobalRemove handles global removal
func (r *Registry) handleGlobalRemove(data []byte) {
	if len(data) < 4 {
		return
	}

	name := binary.LittleEndian.Uint32(data[0:4])

	r.mu.Lock()
	delete(r.globals, name)
	handlers := append([]RegistryGlobalRemoveHandler(nil), r.removeHandlers...)
	r.mu.Unlock()
	for _, handler := range handlers {
		handler.HandleRegistryGlobalRemove(RegistryGlobalRemoveEvent{Registry: r, Name: name})
	}
}

// AddHandler adds a handler for a specific interface
func (r *Registry) AddHandler(iface string, handler GlobalHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[iface] = handler
}

// Bind binds to a global object and returns a typed proxy
func (r *Registry) Bind(name uint32, iface string, version uint32, proxy Proxy) error {
	// Set the ID if not already set
	if proxy.ID() == 0 {
		proxy.SetID(r.display.allocateID())
	}

	// Bootstrap a generated proxy with this display's context.
	if proxy.Context() == nil {
		if baseProxy, ok := proxy.(*BaseProxy); ok {
			baseProxy.SetContext(r.display.Context())
		} else if setter, ok := proxy.(interface{ SetContext(*Context) }); ok {
			setter.SetContext(r.display.Context())
		} else {
			return fmt.Errorf("proxy doesn't have context and can't set it")
		}
	}

	if proxy.Context() != r.display.Context() {
		return fmt.Errorf("proxy belongs to another display")
	}
	if proxy.ID() == displayObjectID || proxy.ID() == r.id {
		return fmt.Errorf("reserved object ID")
	}
	if old, exists := r.display.objects.Load(proxy.ID()); exists && old != proxy {
		return fmt.Errorf("object ID %d already registered", proxy.ID())
	}
	// Register the proxy
	proxy.Context().Register(proxy)

	// Send bind request (opcode 0) with proper arguments
	if err := r.display.SendRequest(r.id, 0, name, iface, version, proxy.ID()); err != nil {
		proxy.Context().Unregister(proxy)
		return err
	}

	return nil
}

// BindID binds to a global object and returns just the ID (compatibility method)
func (r *Registry) BindID(name uint32, iface string, version uint32) (uint32, error) {
	newID := r.display.allocateID()

	// Send bind request (opcode 0) with proper arguments
	if err := r.display.SendRequest(r.id, 0, name, iface, version, newID); err != nil {
		return 0, err
	}

	return newID, nil
}

// GetGlobals returns all announced globals
func (r *Registry) GetGlobals() map[uint32]Global {
	r.mu.RLock()
	defer r.mu.RUnlock()

	globals := make(map[uint32]Global)
	for k, v := range r.globals {
		globals[k] = v
	}
	return globals
}

// FindGlobal finds a global by interface name
func (r *Registry) FindGlobal(iface string) (Global, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, global := range r.globals {
		if global.Interface == iface {
			return global, true
		}
	}
	return Global{}, false
}
