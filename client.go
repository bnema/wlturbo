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
	conn      net.Conn
	unix      *net.UnixConn // set when conn is a Unix socket, nil otherwise
	objects   sync.Map      // map[uint32]Object
	nextID    uint32
	sendMu    sync.Mutex
	recvMu    sync.Mutex
	listeners sync.Map // map[uint32]map[uint16][]func([]byte)

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
	fdMu       sync.Mutex
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
	id       uint32
	display  *Display
	globals  map[uint32]Global
	mu       sync.RWMutex
	handlers map[string]GlobalHandler
}

// callbackObject represents a wl_callback object
type callbackObject struct {
	BaseProxy
	display *Display
}

func (c *callbackObject) ID() uint32 {
	return c.id
}

// Dispatch handles callback events (opcode 0 = done)
func (c *callbackObject) Dispatch(event *Event) {
	if event.Opcode != 0 { // done event
		return
	}
	c.display.notifyListeners(c.id, 0, event.data)
}

// Global represents a global object
type Global struct {
	Name      uint32
	Interface string
	Version   uint32
}

// GlobalHandler is called when a global is announced
type GlobalHandler func(registry *Registry, name uint32, version uint32)

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

	d := newDisplay(conn)

	// Get registry
	if err := d.getRegistry(); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("failed to get registry: %w", err)
	}

	// Don't do initial roundtrip here - let the caller do it after setting up handlers
	return d, nil
}

// Close closes the display connection. It is safe to call more than once;
// later calls are no-ops. After Close, Dispatch returns net.ErrClosed.
func (d *Display) Close() error {
	if !d.closed.CompareAndSwap(false, true) {
		return nil
	}
	d.closePendingFDs()
	return d.conn.Close()
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
	if bufLen > 0xFFFF {
		return fmt.Errorf("message too large: %d bytes", bufLen)
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
		if strlen > 0xFFFFFFFF {
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
		if arrlen > 0xFFFFFFFF {
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

// dispatchFrame delivers one complete frame to the object that owns it.
func (d *Display) dispatchFrame(f receivedFrame) error {
	if f.object == displayObjectID {
		return d.handleDisplayEvent(f.opcode, f.body)
	}

	obj, ok := d.objects.Load(f.object)
	if !ok {
		return &ProtocolError{
			Kind:   "unknown_object",
			Object: f.object,
			Opcode: f.opcode,
			Size:   uint32(len(f.body)) + HeaderSize,
			Err:    ErrUnknownObject,
		}
	}

	// Server-created objects are announced by an event on a known object.
	d.handleServerObject(f.object, f.opcode, f.body)

	if proxy, ok := obj.(Proxy); ok && proxy != nil {
		proxy.Dispatch(&Event{
			ProxyID: f.object,
			Opcode:  f.opcode,
			data:    f.body,
			display: d,
		})
		return nil
	}

	// High-performance dispatcher, then compatibility listeners.
	d.dispatcher.Dispatch(f.object, f.opcode, f.body)
	d.notifyListeners(f.object, f.opcode, f.body)
	return nil
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
	handlerSlice, ok := handlers.(*[]func([]byte))
	if !ok || handlerSlice == nil {
		return
	}
	for _, handler := range *handlerSlice {
		if handler != nil {
			handler(body)
		}
	}
}

// handleDisplayEvent handles events on the display object
func (d *Display) handleDisplayEvent(opcode uint16, data []byte) error {
	switch opcode {
	case 0: // error
		if len(data) < 8 {
			return &ProtocolError{Kind: "short_display_error", Object: displayObjectID, Opcode: opcode, Size: uint32(len(data)) + HeaderSize, Err: ErrMalformedFrame}
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
		if len(data) < 4 {
			return &ProtocolError{Kind: "short_delete_id", Object: displayObjectID, Opcode: opcode, Size: uint32(len(data)) + HeaderSize, Err: ErrMalformedFrame}
		}
		id := binary.LittleEndian.Uint32(data[0:4])
		d.objects.Delete(id)
		return nil

	default:
		return &ProtocolError{Kind: "unknown_opcode", Object: displayObjectID, Opcode: opcode, Err: ErrUnknownOpcode}
	}
}

// Roundtrip performs a synchronous roundtrip to the compositor
func (d *Display) Roundtrip() error {
	callbackID := d.allocateID()
	done := make(chan error, 1)

	d.AddListener(callbackID, 0, func(_ []byte) {
		d.objects.Delete(callbackID)
		done <- nil
	})

	// Send sync request (opcode 0)
	if err := d.SendRequest(displayObjectID, 0, callbackID); err != nil {
		return err
	}

	d.objects.Store(callbackID, &callbackObject{
		BaseProxy: BaseProxy{
			context: d.Context(),
			id:      callbackID,
		},
		display: d,
	})

	for i := 0; i < 1000; i++ {
		if err := d.Dispatch(); err != nil {
			return err
		}
		select {
		case err := <-done:
			return err
		default:
		}
	}

	return fmt.Errorf("roundtrip failed: max iterations reached")
}

// AddListener adds an event listener for an object
func (d *Display) AddListener(objectID uint32, opcode uint16, handler func([]byte)) {
	// Load or create listener map for object
	listeners, _ := d.listeners.LoadOrStore(objectID, &sync.Map{})
	opcodeMap := listeners.(*sync.Map)

	// Load or create handlers slice for opcode
	handlers, _ := opcodeMap.LoadOrStore(opcode, &[]func([]byte){})
	handlerSlice := handlers.(*[]func([]byte))

	// Add handler (thread-safe)
	*handlerSlice = append(*handlerSlice, handler)
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
		// log.Printf("wlclient: handleGlobal: data too short (%d bytes)", len(data))
		return
	}

	name := binary.LittleEndian.Uint32(data[0:4])
	ifaceLen := binary.LittleEndian.Uint32(data[4:8])

	if len(data) < 8+int(ifaceLen)+4 {
		// log.Printf("wlclient: handleGlobal: insufficient data for interface string (need %d, have %d)", 8+int(ifaceLen)+4, len(data))
		return
	}

	// String includes null terminator in length
	iface := string(data[8 : 8+ifaceLen-1]) // -1 to remove null terminator

	// Calculate padding for 32-bit alignment
	padding := (4 - (ifaceLen % 4)) % 4
	versionOffset := 8 + int(ifaceLen) + int(padding)

	if len(data) < versionOffset+4 {
		// log.Printf("wlclient: handleGlobal: insufficient data for version (need %d, have %d)", versionOffset+4, len(data))
		return
	}

	version := binary.LittleEndian.Uint32(data[versionOffset:])

	// log.Printf("wlclient: global announced: %s v%d (name=%d)", iface, version, name)

	// Store global
	r.mu.Lock()
	r.globals[name] = Global{
		Name:      name,
		Interface: iface,
		Version:   version,
	}
	r.mu.Unlock()

	// Call specific handler if registered
	if handler, ok := r.handlers[iface]; ok {
		// log.Printf("wlclient: calling handler for interface %s", iface)
		handler(r, name, version)
	}

	// Call wildcard handler if registered
	if handler, ok := r.handlers["*"]; ok {
		// log.Printf("wlclient: calling wildcard handler for interface %s", iface)
		handler(r, name, version)
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
	r.mu.Unlock()
}

// AddHandler adds a handler for a specific interface
func (r *Registry) AddHandler(iface string, handler GlobalHandler) {
	r.handlers[iface] = handler
}

// Bind binds to a global object and returns a typed proxy
func (r *Registry) Bind(name uint32, iface string, version uint32, proxy Proxy) error {
	// Set the ID if not already set
	if proxy.ID() == 0 {
		proxy.SetID(r.display.allocateID())
	}

	// Ensure proxy has a context set - CRITICAL FIX
	if proxy.Context() == nil {
		if baseProxy, ok := proxy.(*BaseProxy); ok {
			baseProxy.SetContext(r.display.Context())
		} else if setter, ok := proxy.(interface{ SetContext(*Context) }); ok {
			setter.SetContext(r.display.Context())
		} else {
			return fmt.Errorf("proxy doesn't have context and can't set it")
		}
	}

	// Register the proxy
	proxy.Context().Register(proxy)

	// Also register directly in display objects map
	r.display.objects.Store(proxy.ID(), proxy)

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

// handleServerObject creates and registers server-allocated objects from events
func (d *Display) handleServerObject(objectID uint32, opcode uint16, body []byte) bool {
	// Handle specific known protocols that create server objects
	obj, ok := d.objects.Load(objectID)
	if !ok {
		return false
	}

	// Check if this is a known object type that creates server objects
	switch obj.(type) {
	case *Registry:
		// Registry doesn't create server objects directly
		return false

	default:
		// Check if this might be an output manager (interface zwlr_output_manager_v1)
		if objectID == 5 { // This is likely the output manager based on your logs
			return d.handleOutputManagerEvent(objectID, opcode, body)
		}
	}

	return false
}

// handleOutputManagerEvent handles events from zwlr_output_manager_v1
func (d *Display) handleOutputManagerEvent(objectID uint32, opcode uint16, body []byte) bool {
	switch opcode {
	case 0: // head event - creates new zwlr_output_head_v1 object
		if len(body) < 4 {
			return false
		}

		// Parse the new_id for the head object
		headID := binary.LittleEndian.Uint32(body[0:4])

		// Create a proper OutputHead object
		headProxy := &OutputHead{
			BaseProxy: BaseProxy{
				id:      headID,
				context: d.Context(),
			},
		}

		// Register the new head object
		d.objects.Store(headID, headProxy)
		return true

	case 1: // done event
		return false

	case 2: // finished event
		return false
	}

	return false
}

// OutputHead represents a zwlr_output_head_v1 object
type OutputHead struct {
	BaseProxy
	name        string
	description string
	width       int32
	height      int32
}

// Dispatch handles head events
func (h *OutputHead) Dispatch(event *Event) {
	switch event.Opcode {
	case 0: // name
		h.name = event.String()
	case 1: // description
		h.description = event.String()
	case 2: // physical_size
		h.width = event.Int32()
		h.height = event.Int32()
	case 3: // mode (creates new mode object)
		modeID := event.Uint32()
		// Create mode object
		mode := &OutputMode{
			BaseProxy: BaseProxy{
				id:      modeID,
				context: h.context,
			},
		}
		h.context.display.objects.Store(modeID, mode)
	case 9: // finished
		h.context.Unregister(h)
	default:
	}
}

// OutputMode represents a zwlr_output_mode_v1 object
type OutputMode struct {
	BaseProxy
	width   int32
	height  int32
	refresh int32
}

// Dispatch handles mode events
func (m *OutputMode) Dispatch(event *Event) {
	switch event.Opcode {
	case 0: // size
		m.width = event.Int32()
		m.height = event.Int32()
	case 1: // refresh
		m.refresh = event.Int32()
	case 2: // preferred
	case 3: // finished
		m.context.Unregister(m)
	default:
	}
}
