package wlturbo

import (
	"encoding/binary"
	"errors"

	// "log"
	"sync"
	"sync/atomic"
)

// Context provides a compatibility layer for wl.Context
type Context struct {
	display *Display
	proxies sync.Map // map[uint32]Proxy
	closed  atomic.Bool
}

// Proxy interface for Wayland protocol objects
type Proxy interface {
	Object
	SetID(uint32)
	Context() *Context
	Dispatch(*Event)
}

// BaseProxy provides base implementation for protocol objects
type BaseProxy struct {
	id      uint32
	context *Context
}

// Event represents a Wayland protocol event
type Event struct {
	ProxyID uint32
	Opcode  uint16
	data    []byte
	offset  int
}

// Data returns the raw event data
func (e *Event) Data() []byte {
	return e.data
}

// Offset returns the current read offset
func (e *Event) Offset() int {
	return e.offset
}

// NewContext creates a new context from a display
func NewContext(display *Display) *Context {
	return &Context{
		display: display,
	}
}

// SendRequest sends a request through the context
func (c *Context) SendRequest(proxy Proxy, opcode uint32, args ...interface{}) error {
	if c.closed.Load() {
		return errors.New("context is closed")
	}
	return c.display.SendRequest(proxy.ID(), uint16(opcode), args...)
}

// SendRequestWithFDs sends a request with file descriptors through the context
func (c *Context) SendRequestWithFDs(proxy Proxy, opcode uint32, fds []int, args ...interface{}) error {
	if c.closed.Load() {
		return errors.New("context is closed")
	}
	return c.display.SendRequestWithFDs(proxy.ID(), uint16(opcode), fds, args...)
}

// Register registers a proxy object
func (c *Context) Register(proxy Proxy) {
	if proxy != nil && proxy.ID() != 0 {
		c.proxies.Store(proxy.ID(), proxy)
		c.display.objects.Store(proxy.ID(), proxy)
		// Debug large IDs
		// if proxy.ID() > 1000000 {
		// 	log.Printf("wlclient: WARNING: Registering proxy with suspicious ID %d (0x%x)", proxy.ID(), proxy.ID())
		// } else if proxy.ID() == 5 {
		// 	log.Printf("wlclient: Registered output manager proxy with ID 5")
		// }
	}
}

// Unregister removes a proxy object
func (c *Context) Unregister(proxy Proxy) {
	if proxy != nil {
		c.proxies.Delete(proxy.ID())
		c.display.objects.Delete(proxy.ID())
	}
}

// UnregisterID removes a proxy object by ID (overloaded for compatibility)
func (c *Context) UnregisterID(id uint32) {
	c.proxies.Delete(id)
	c.display.objects.Delete(id)
}

// AllocateID allocates a new object ID
func (c *Context) AllocateID() uint32 {
	return c.display.AllocateID()
}

// Close closes the context
func (c *Context) Close() error {
	c.closed.Store(true)
	return c.display.Close()
}

// RunTill runs the event loop until the callback fires
func (c *Context) RunTill(callback Object) error {
	if c.closed.Load() {
		return errors.New("context is closed")
	}

	// Special case: if callback is a sync object, do a roundtrip
	if _, ok := callback.(*callbackObject); ok {
		return c.display.Roundtrip()
	}

	// Otherwise, process events until the callback is unregistered
	// This assumes the callback will unregister itself when done
	for {
		if err := c.display.Dispatch(); err != nil {
			return err
		}

		// Check if callback is still registered
		if _, ok := c.proxies.Load(callback.ID()); !ok {
			// Callback has been unregistered, we're done
			return nil
		}
	}
}

// BaseProxy methods

// ID returns the proxy's object ID
func (p *BaseProxy) ID() uint32 {
	return p.id
}

// SetId sets the proxy's object ID
func (p *BaseProxy) SetID(id uint32) {
	p.id = id
}

// Context returns the proxy's context
func (p *BaseProxy) Context() *Context {
	return p.context
}

// SetContext sets the proxy's context
func (p *BaseProxy) SetContext(ctx *Context) {
	p.context = ctx
}

// Dispatch default implementation (does nothing)
func (p *BaseProxy) Dispatch(event *Event) {
	// Default implementation does nothing
}

// Event methods for extracting data

// Uint32 reads a uint32 from the event
func (e *Event) Uint32() uint32 {
	if e.offset+4 > len(e.data) {
		return 0
	}
	val := binary.LittleEndian.Uint32(e.data[e.offset:])
	e.offset += 4
	return val
}

// Int32 reads an int32 from the event
func (e *Event) Int32() int32 {
	return int32(e.Uint32())
}

// Fixed reads a fixed-point value from the event
func (e *Event) Fixed() Fixed {
	return Fixed(e.Int32())
}

// String reads a string from the event
func (e *Event) String() string {
	if e.offset+4 > len(e.data) {
		return ""
	}
	strlen := e.Uint32()
	if strlen == 0 || e.offset+int(strlen) > len(e.data) {
		return ""
	}
	// String includes null terminator in length
	str := string(e.data[e.offset : e.offset+int(strlen)-1])
	// Advance offset including padding
	totalLen := strlen
	padding := (4 - (totalLen % 4)) % 4
	e.offset += int(totalLen + padding)
	return str
}

// Array reads a byte array from the event
func (e *Event) Array() []byte {
	if e.offset+4 > len(e.data) {
		return nil
	}
	arrlen := e.Uint32()
	if arrlen == 0 || e.offset+int(arrlen) > len(e.data) {
		return nil
	}
	arr := make([]byte, arrlen)
	copy(arr, e.data[e.offset:e.offset+int(arrlen)])
	// Advance offset including padding
	padding := (4 - (arrlen % 4)) % 4
	e.offset += int(arrlen + padding)
	return arr
}

// Fd reads a file descriptor from the event
func (e *Event) Fd() uintptr {
	// File descriptors are passed out-of-band via SCM_RIGHTS
	// We read them from our lock-free queue
	if fd, ok := GetNextFD(); ok {
		return uintptr(fd)
	}
	// Fallback: read placeholder value from message
	_ = e.Uint32()
	return 0
}

// NewId reads a new object ID from the event
func (e *Event) NewID() Proxy {
	id := e.Uint32()
	// Return a basic proxy with just the ID
	return &BaseProxy{id: id}
}

// Proxy reads an existing proxy reference from the event
func (e *Event) Proxy() Proxy {
	id := e.Uint32()
	// Would need context reference to look up actual proxy
	return &BaseProxy{id: id}
}

// Registry handler interfaces for compatibility

// RegistryGlobalHandler interface
type RegistryGlobalHandler interface {
	HandleRegistryGlobal(event RegistryGlobalEvent)
}

// RegistryGlobalRemoveHandler interface
type RegistryGlobalRemoveHandler interface {
	HandleRegistryGlobalRemove(event RegistryGlobalRemoveEvent)
}

// RegistryGlobalEvent represents a registry global announcement
type RegistryGlobalEvent struct {
	Registry  *Registry
	Name      uint32
	Interface string
	Version   uint32
}

// RegistryGlobalRemoveEvent represents a registry global removal
type RegistryGlobalRemoveEvent struct {
	Registry *Registry
	Name     uint32
}

// AddGlobalHandler adds a global handler to the registry
func (r *Registry) AddGlobalHandler(handler RegistryGlobalHandler) {
	// Store the handler to be called for ALL globals
	r.mu.Lock()
	defer r.mu.Unlock()

	// Initialize handlers map if needed
	if r.handlers == nil {
		r.handlers = make(map[string]GlobalHandler)
	}

	r.AddHandler("*", func(registry *Registry, name uint32, version uint32) {
		global, ok := r.FindGlobalByName(name)
		if ok {
			event := RegistryGlobalEvent{
				Registry:  r,
				Name:      name,
				Interface: global.Interface,
				Version:   version,
			}
			handler.HandleRegistryGlobal(event)
		}
	})
}

// AddGlobalRemoveHandler adds a global remove handler (placeholder)
func (r *Registry) AddGlobalRemoveHandler(handler RegistryGlobalRemoveHandler) {
	// Would need to implement removal handling
}

// FindGlobalByName finds a global by its name ID
func (r *Registry) FindGlobalByName(name uint32) (Global, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if global, ok := r.globals[name]; ok {
		return global, true
	}
	return Global{}, false
}

// Display compatibility methods

// Context returns a context for this display
func (d *Display) Context() *Context {
	return d.context
}

// GetRegistry returns the registry (compatibility)
func (d *Display) GetRegistry() *Registry {
	return d.registry
}

// Sync creates a sync callback
func (d *Display) Sync() (Object, error) {
	callbackID := d.allocateID()
	callback := &callbackObject{
		BaseProxy: BaseProxy{
			context: d.context,
			id:      callbackID,
		},
		display: d,
	}

	// Send sync request (opcode 0)
	if err := d.SendRequest(1, 0, callbackID); err != nil {
		return nil, err
	}

	// Store the callback object
	d.objects.Store(callbackID, callback)

	return callback, nil
}

// Additional protocol object types needed for compatibility

// Seat capability constants
const (
	SeatCapabilityPointer  = 1
	SeatCapabilityKeyboard = 2
	SeatCapabilityTouch    = 4
)

// Seat represents a wl_seat
type Seat struct {
	BaseProxy
	capabilities uint32
	name         string
}

// SeatCapabilitiesHandler handles seat capabilities events
type SeatCapabilitiesHandler interface {
	HandleSeatCapabilities(seat *Seat, capabilities uint32)
}

// SeatNameHandler handles seat name events
type SeatNameHandler interface {
	HandleSeatName(seat *Seat, name string)
}

// NewSeat creates a new seat proxy
func NewSeat(ctx *Context) *Seat {
	return &Seat{
		BaseProxy: BaseProxy{
			context: ctx,
		},
	}
}

// GetPointer gets the pointer device
func (s *Seat) GetPointer() (*Pointer, error) {
	pointer := &Pointer{
		BaseProxy: BaseProxy{
			context: s.context,
			id:      s.context.display.allocateID(),
		},
	}

	// Register pointer before sending request
	s.context.Register(pointer)

	// Send get_pointer request (opcode 0)
	err := s.context.SendRequest(s, 0, pointer.id)
	if err != nil {
		s.context.Unregister(pointer)
		return nil, err
	}

	return pointer, nil
}

// GetKeyboard gets the keyboard device
func (s *Seat) GetKeyboard() (*Keyboard, error) {
	keyboard := &Keyboard{
		BaseProxy: BaseProxy{
			context: s.context,
			id:      s.context.display.allocateID(),
		},
	}

	// Register keyboard before sending request
	s.context.Register(keyboard)

	// Send get_keyboard request (opcode 1)
	err := s.context.SendRequest(s, 1, keyboard.id)
	if err != nil {
		s.context.Unregister(keyboard)
		return nil, err
	}

	return keyboard, nil
}

// GetTouch gets the touch device
func (s *Seat) GetTouch() (*Touch, error) {
	touch := &Touch{
		BaseProxy: BaseProxy{
			context: s.context,
			id:      s.context.display.allocateID(),
		},
	}

	// Register touch before sending request
	s.context.Register(touch)

	// Send get_touch request (opcode 2)
	err := s.context.SendRequest(s, 2, touch.id)
	if err != nil {
		s.context.Unregister(touch)
		return nil, err
	}

	return touch, nil
}

// Release releases the seat
func (s *Seat) Release() error {
	err := s.context.SendRequest(s, 3) // opcode 3
	if err == nil {
		s.context.Unregister(s)
	}
	return err
}

// Capabilities returns the seat capabilities
func (s *Seat) Capabilities() uint32 {
	return s.capabilities
}

// Name returns the seat name
func (s *Seat) Name() string {
	return s.name
}

// Dispatch handles events for the seat
func (s *Seat) Dispatch(event *Event) {
	// Handle seat events
	switch event.Opcode {
	case 0: // capabilities
		s.capabilities = event.Uint32()
		// TODO: Call registered handlers
	case 1: // name
		s.name = event.String()
		// TODO: Call registered handlers
	}
}

// Surface represents a wl_surface
type Surface struct {
	BaseProxy
}

// NewSurface creates a new surface proxy
func NewSurface(ctx *Context) *Surface {
	return &Surface{
		BaseProxy: BaseProxy{
			context: ctx,
		},
	}
}

// Destroy destroys the surface
func (s *Surface) Destroy() error {
	err := s.context.SendRequest(s, 0) // opcode 0
	if err == nil {
		s.context.Unregister(s)
	}
	return err
}

// Attach attaches a buffer to the surface
func (s *Surface) Attach(buffer Object, x, y int32) error {
	return s.context.SendRequest(s, 1, buffer, x, y) // opcode 1
}

// Damage marks a region of the surface as damaged
func (s *Surface) Damage(x, y, width, height int32) error {
	return s.context.SendRequest(s, 2, x, y, width, height) // opcode 2
}

// Frame requests a frame callback
func (s *Surface) Frame() (Object, error) {
	callback := &callbackObject{
		BaseProxy: BaseProxy{
			context: s.context,
			id:      s.context.display.allocateID(),
		},
		display: s.context.display,
	}
	s.context.Register(callback)

	err := s.context.SendRequest(s, 3, callback.id) // opcode 3
	if err != nil {
		s.context.Unregister(callback)
		return nil, err
	}

	return callback, nil
}

// SetOpaqueRegion sets the opaque region
func (s *Surface) SetOpaqueRegion(region *Region) error {
	return s.context.SendRequest(s, 4, region) // opcode 4
}

// SetInputRegion sets the input region
func (s *Surface) SetInputRegion(region *Region) error {
	return s.context.SendRequest(s, 5, region) // opcode 5
}

// Commit commits pending surface state
func (s *Surface) Commit() error {
	return s.context.SendRequest(s, 6) // opcode 6
}

// SetBufferTransform sets the buffer transform
func (s *Surface) SetBufferTransform(transform int32) error {
	return s.context.SendRequest(s, 7, transform) // opcode 7
}

// SetBufferScale sets the buffer scale
func (s *Surface) SetBufferScale(scale int32) error {
	return s.context.SendRequest(s, 8, scale) // opcode 8
}

// DamageBuffer marks a region of the buffer as damaged
func (s *Surface) DamageBuffer(x, y, width, height int32) error {
	return s.context.SendRequest(s, 9, x, y, width, height) // opcode 9
}

// Offset sets the buffer offset
func (s *Surface) Offset(x, y int32) error {
	return s.context.SendRequest(s, 10, x, y) // opcode 10
}

// Dispatch handles surface events
func (s *Surface) Dispatch(event *Event) {
	// Surface can receive enter/leave/preferred_buffer_scale/preferred_buffer_transform events
	// But for now we don't handle them
}

// Pointer represents a wl_pointer
type Pointer struct {
	BaseProxy
}

// Keyboard represents a wl_keyboard
type Keyboard struct {
	BaseProxy
}

// Touch represents a wl_touch
type Touch struct {
	BaseProxy
}

// Output represents a wl_output
type Output struct {
	BaseProxy
}

// Region represents a wl_region
type Region struct {
	BaseProxy
}

// Add adds a rectangle to the region
func (r *Region) Add(x, y, width, height int32) error {
	return r.context.SendRequest(r, 0, x, y, width, height) // opcode 0
}

// Subtract subtracts a rectangle from the region
func (r *Region) Subtract(x, y, width, height int32) error {
	return r.context.SendRequest(r, 1, x, y, width, height) // opcode 1
}

// Destroy destroys the region
func (r *Region) Destroy() error {
	err := r.context.SendRequest(r, 2) // opcode 2
	if err == nil {
		r.context.Unregister(r)
	}
	return err
}

// Compositor represents a wl_compositor
type Compositor struct {
	BaseProxy
}

// NewCompositor creates a new compositor proxy
func NewCompositor(ctx *Context) *Compositor {
	return &Compositor{
		BaseProxy: BaseProxy{
			context: ctx,
		},
	}
}

// CreateSurface creates a new surface
func (c *Compositor) CreateSurface() (*Surface, error) {
	surface := &Surface{
		BaseProxy: BaseProxy{
			context: c.context,
			id:      c.context.display.allocateID(),
		},
	}

	// Register surface before sending request
	c.context.Register(surface)

	// Send create_surface request (opcode 0)
	err := c.context.SendRequest(c, 0, surface.id)
	if err != nil {
		c.context.Unregister(surface)
		return nil, err
	}

	return surface, nil
}

// CreateRegion creates a new region
func (c *Compositor) CreateRegion() (*Region, error) {
	region := &Region{
		BaseProxy: BaseProxy{
			context: c.context,
			id:      c.context.display.allocateID(),
		},
	}

	// Register region before sending request
	c.context.Register(region)

	// Send create_region request (opcode 1)
	err := c.context.SendRequest(c, 1, region.id)
	if err != nil {
		c.context.Unregister(region)
		return nil, err
	}

	return region, nil
}
