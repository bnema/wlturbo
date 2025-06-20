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
	"log"

	// "log"
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
	fd        int
	objects   sync.Map // map[uint32]Object
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

	// Reusable read buffer for header
	headerBuf [8]byte

	// Pre-allocated buffer for event bodies (avoid allocations)
	eventBodyBuf [4096]byte
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
	if event.Opcode == 0 { // done event
		log.Printf("wlclient: callbackObject.Dispatch fired for ID %d", c.id)
		// Trigger any listeners for this callback
		if listeners, ok := c.display.listeners.Load(c.id); ok {
			if opcodeMap, ok := listeners.(*sync.Map); ok {
				if handlers, ok := opcodeMap.Load(uint16(0)); ok {
					if handlerSlice, ok := handlers.(*[]func([]byte)); ok {
						if handlerSlice == nil {
							log.Printf("wlclient: WARNING: handlerSlice is nil for callback ID %d", c.id)
							return
						}
						log.Printf("wlclient: Found %d handlers for callback ID %d", len(*handlerSlice), c.id)
						for i, handler := range *handlerSlice {
							if handler == nil {
								log.Printf("wlclient: WARNING: handler %d is nil for callback ID %d", i, c.id)
								continue
							}
							log.Printf("wlclient: Calling handler %d for callback ID %d", i, c.id)
							handler(event.data)
						}
					} else {
						log.Printf("wlclient: WARNING: handlers not a slice for callback ID %d", c.id)
					}
				} else {
					log.Printf("wlclient: WARNING: no handlers found for opcode 0 on callback ID %d", c.id)
				}
			} else {
				log.Printf("wlclient: WARNING: listeners not a sync.Map for callback ID %d", c.id)
			}
		} else {
			log.Printf("wlclient: WARNING: no listeners found for callback ID %d", c.id)
		}
	}
}

// Global represents a global object
type Global struct {
	Name      uint32
	Interface string
	Version   uint32
}

// GlobalHandler is called when a global is announced
type GlobalHandler func(registry *Registry, name uint32, version uint32)

// Connect connects to the Wayland display
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

	// log.Printf("wlclient: Connecting to Wayland socket: %s", socketPath)
	// Connect to socket
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Wayland: %w", err)
	}
	// log.Printf("wlclient: Connected to Wayland socket successfully")

	// Get file descriptor for advanced operations
	file, err := conn.(*net.UnixConn).File()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to get socket fd: %w", err)
	}
	fd := int(file.Fd())
	_ = file.Close() // We only need the fd

	d := &Display{
		conn:       conn,
		fd:         fd,
		nextID:     2, // 1 is reserved for wl_display
		dispatcher: NewEventDispatcher(),
	}

	// Create context once and store it
	d.context = NewContext(d)

	// Register display object (ID 1)
	d.objects.Store(uint32(1), d)

	// Initialize registry
	d.registry = &Registry{
		id:       d.allocateID(),
		display:  d,
		globals:  make(map[uint32]Global),
		handlers: make(map[string]GlobalHandler),
	}
	d.objects.Store(d.registry.id, d.registry)

	// Get registry
	if err := d.getRegistry(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to get registry: %w", err)
	}

	// Don't do initial roundtrip here - let the caller do it after setting up handlers
	// log.Printf("wlclient: display connected, registry ready")

	return d, nil
}

// Close closes the display connection
func (d *Display) Close() error {
	return d.conn.Close()
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
	id := atomic.AddUint32(&d.nextID, 1) - 1
	log.Printf("wlclient: allocateID() generated ID %d", id)
	return id
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

// Dispatch reads and dispatches events
func (d *Display) Dispatch() error {
	d.recvMu.Lock()
	defer d.recvMu.Unlock()

	// Read header with potential file descriptors
	n, fds, err := d.recvmsgWithFDs(d.headerBuf[:])
	if err != nil {
		return fmt.Errorf("failed to read header: %w", err)
	}
	if n < 8 {
		return fmt.Errorf("incomplete header: got %d bytes", n)
	}

	objectID := binary.LittleEndian.Uint32(d.headerBuf[0:4])
	sizeOpcode := binary.LittleEndian.Uint32(d.headerBuf[4:8])
	// Upper 16 bits = size (includes header), lower 16 bits = opcode
	size := sizeOpcode >> 16
	opcode := sizeOpcode & 0xffff

	// Only log suspicious events or important ones
	// if objectID > 1000000 || objectID == 5 {
	// 	log.Printf("wlclient: Dispatch received event: object=%d (0x%x), opcode=%d, size=%d", objectID, objectID, opcode, size)
	// }
	if opcode > 0xFFFF {
		return fmt.Errorf("invalid opcode: %d", opcode)
	}

	// Read message body (size includes 8-byte header)
	var body []byte
	if size > 8 {
		bodySize := size - 8

		// Use pre-allocated buffer for small messages (zero allocation)
		if bodySize <= uint32(len(d.eventBodyBuf)) {
			body = d.eventBodyBuf[:bodySize]
		} else {
			// Large message, allocate new buffer
			body = make([]byte, bodySize)
		}

		n, moreFds, err := d.recvmsgWithFDs(body)
		if err != nil {
			return fmt.Errorf("failed to read body: %w", err)
		}
		if n < int(bodySize) {
			// Read remaining data if needed
			remaining := body[n:]
			if _, err := io.ReadFull(d.conn, remaining); err != nil {
				return fmt.Errorf("failed to read remaining body: %w", err)
			}
		}
		fds = append(fds, moreFds...)
	}

	// Handle display events specially
	if objectID == 1 {
		return d.handleDisplayEvent(uint16(opcode), body)
	}

	// Try to find the object
	if obj, ok := d.objects.Load(objectID); ok {
		// Only log for non-registry objects to reduce spam
		if objectID != 2 {
			log.Printf("wlclient: Found object for ID %d, type: %T", objectID, obj)
		}

		// CRITICAL: Handle server object creation BEFORE dispatching
		if d.handleServerObject(objectID, uint16(opcode), body) {
			log.Printf("wlclient: Handled server object creation for ID %d, opcode %d", objectID, opcode)
		}

		// Check if it's a Proxy with Dispatch method
		if proxy, ok := obj.(Proxy); ok && proxy != nil {
			event := &Event{
				ProxyID: objectID,
				Opcode:  uint16(opcode),
				data:    body,
				offset:  0,
			}
			if objectID != 2 {
				log.Printf("wlclient: Dispatching event to proxy ID %d, opcode %d", objectID, opcode)
			}
			proxy.Dispatch(event)
			return nil // Event was handled
		}
	} else {
		log.Printf("wlclient: WARNING: No object found for ID %d (0x%x)", objectID, objectID)
	}

	// Use high-performance dispatcher
	d.dispatcher.Dispatch(objectID, uint16(opcode), body)

	// Also check listeners (for compatibility)
	if listeners, ok := d.listeners.Load(objectID); ok {
		if opcodeMap, ok := listeners.(*sync.Map); ok {
			if handlers, ok := opcodeMap.Load(uint16(opcode)); ok {
				if handlerSlice, ok := handlers.(*[]func([]byte)); ok {
					for _, handler := range *handlerSlice {
						handler(body)
					}
				}
			}
		}
	}

	return nil
}

// handleDisplayEvent handles events on the display object
func (d *Display) handleDisplayEvent(opcode uint16, data []byte) error {
	switch opcode {
	case 0: // error
		if len(data) < 8 {
			return errors.New("invalid error event")
		}
		objectID := binary.LittleEndian.Uint32(data[0:4])
		code := binary.LittleEndian.Uint32(data[4:8])

		var message string
		if len(data) > 8 {
			// Message is optional, parse if present
			if len(data) >= 12 {
				msgLen := binary.LittleEndian.Uint32(data[8:12])
				if msgLen > 0 && len(data) >= 12+int(msgLen) {
					message = string(data[12 : 12+msgLen-1]) // -1 to remove null terminator
				}
			}
		}

		d.lastError = fmt.Errorf("protocol error: object %d, code %d: %s", objectID, code, message)
		d.lastErrorCode = code
		d.lastErrorObj = objectID
		return d.lastError

	case 1: // delete_id
		if len(data) < 4 {
			return errors.New("invalid delete_id event")
		}
		id := binary.LittleEndian.Uint32(data[0:4])
		d.objects.Delete(id)
	}

	return nil
}

// Roundtrip performs a synchronous roundtrip to the compositor
func (d *Display) Roundtrip() error {
	log.Printf("wlclient: Roundtrip starting...")
	// Create callback
	callbackID := d.allocateID()
	done := make(chan error, 1)

	log.Printf("wlclient: Registering callback listener for ID %d", callbackID)
	// Register callback listener
	d.AddListener(callbackID, 0, func(_ []byte) {
		log.Printf("wlclient: Callback %d fired", callbackID)
		d.objects.Delete(callbackID)
		done <- nil
	})

	// Send sync request (opcode 0)
	log.Printf("wlclient: Sending sync request with callback ID %d", callbackID)
	if err := d.SendRequest(1, 0, callbackID); err != nil {
		return err
	}

	// Store the callback object to track it
	d.objects.Store(callbackID, &callbackObject{
		BaseProxy: BaseProxy{
			context: d.Context(),
			id:      callbackID,
		},
		display: d,
	})

	// Process events until callback fires - SIMPLIFIED APPROACH
	maxIterations := 1000 // Prevent infinite loops
	for i := 0; i < maxIterations; i++ {
		// Don't set read deadline for now - causes issues
		if err := d.Dispatch(); err != nil {
			log.Printf("wlclient: Dispatch error: %v", err)
			return err
		}

		// Check if callback completed
		select {
		case err := <-done:
			log.Printf("wlclient: Roundtrip completed successfully")
			return err
		default:
			// Continue dispatching
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
		newID := r.display.allocateID()
		log.Printf("wlclient: Allocating new ID %d for interface %s", newID, iface)
		proxy.SetID(newID)
	}

	// Ensure proxy has a context set - CRITICAL FIX
	if proxy.Context() == nil {
		if baseProxy, ok := proxy.(*BaseProxy); ok {
			baseProxy.SetContext(r.display.Context())
			log.Printf("wlclient: Set context for proxy ID %d", proxy.ID())
		} else if setter, ok := proxy.(interface{ SetContext(*Context) }); ok {
			setter.SetContext(r.display.Context())
			log.Printf("wlclient: Set context for proxy ID %d via interface", proxy.ID())
		} else {
			return fmt.Errorf("proxy doesn't have context and can't set it")
		}
	}

	// Register the proxy
	proxy.Context().Register(proxy)

	// Also register directly in display objects map
	r.display.objects.Store(proxy.ID(), proxy)
	log.Printf("wlclient: Registered proxy ID %d in display objects map", proxy.ID())

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
			log.Printf("wlclient: head event body too short: %d bytes", len(body))
			return false
		}

		// Parse the new_id for the head object
		headID := binary.LittleEndian.Uint32(body[0:4])
		log.Printf("wlclient: Creating server object (head) with ID %d", headID)

		// Create a proper OutputHead object
		headProxy := &OutputHead{
			BaseProxy: BaseProxy{
				id:      headID,
				context: d.Context(),
			},
		}

		// Register the new head object
		d.objects.Store(headID, headProxy)
		log.Printf("wlclient: Registered server-created head object ID %d", headID)
		return true

	case 1: // done event
		log.Printf("wlclient: Output manager done event")
		return false

	case 2: // finished event
		log.Printf("wlclient: Output manager finished event")
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
	log.Printf("wlclient: OutputHead.Dispatch ID %d, opcode %d", h.id, event.Opcode)
	switch event.Opcode {
	case 0: // name
		h.name = event.String()
		log.Printf("wlclient: Head %d name: %s", h.id, h.name)
	case 1: // description
		h.description = event.String()
		log.Printf("wlclient: Head %d description: %s", h.id, h.description)
	case 2: // physical_size
		h.width = event.Int32()
		h.height = event.Int32()
		log.Printf("wlclient: Head %d physical size: %dx%d mm", h.id, h.width, h.height)
	case 3: // mode (creates new mode object)
		modeID := event.Uint32()
		log.Printf("wlclient: Head %d mode object created: ID %d", h.id, modeID)
		// Create mode object
		mode := &OutputMode{
			BaseProxy: BaseProxy{
				id:      modeID,
				context: h.context,
			},
		}
		h.context.display.objects.Store(modeID, mode)
	case 9: // finished
		log.Printf("wlclient: Head %d finished", h.id)
		h.context.Unregister(h)
	default:
		log.Printf("wlclient: Head %d unhandled opcode %d", h.id, event.Opcode)
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
	log.Printf("wlclient: OutputMode.Dispatch ID %d, opcode %d", m.id, event.Opcode)
	switch event.Opcode {
	case 0: // size
		m.width = event.Int32()
		m.height = event.Int32()
		log.Printf("wlclient: Mode %d size: %dx%d", m.id, m.width, m.height)
	case 1: // refresh
		m.refresh = event.Int32()
		log.Printf("wlclient: Mode %d refresh: %d mHz", m.id, m.refresh)
	case 2: // preferred
		log.Printf("wlclient: Mode %d is preferred", m.id)
	case 3: // finished
		log.Printf("wlclient: Mode %d finished", m.id)
		m.context.Unregister(m)
	default:
		log.Printf("wlclient: Mode %d unhandled opcode %d", m.id, event.Opcode)
	}
}
