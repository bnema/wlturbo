package wlturbo

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/unix"
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
	version uint32
}

// eventPool recycles Events across dispatches; an Event is only valid for
// the duration of the handler call.
var eventPool = sync.Pool{New: func() any { return &Event{} }}

// Event represents a Wayland protocol event
type Event struct {
	ProxyID uint32
	Opcode  uint16
	data    []byte
	offset  int
	display *Display
	fds     []*OwnedFD
	fdIndex int
}

// Data returns the raw event body. It aliases the connection's receive buffer
// and is only valid until the handler returns; copy it to keep it.
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

// SendRequest sends a request through the context. The proxy is validated
// under the connection send lock, so no request reaches the wire after a
// destructor for the same proxy.
func (c *Context) SendRequest(proxy Proxy, opcode uint32, args ...interface{}) error {
	return c.SendRequestWithFDs(proxy, opcode, nil, args...)
}

// SendRequestWithFDs sends a request with file descriptors through the context.
// On error the FDs remain owned by the caller.
func (c *Context) SendRequestWithFDs(proxy Proxy, opcode uint32, fds []int, args ...interface{}) error {
	if err := c.CheckProxy(proxy); err != nil {
		return err
	}
	return c.display.sendRequest(proxy.ID(), uint16(opcode), fds, func() error { return c.CheckProxy(proxy) }, args)
}

// CheckProxy rejects stale and foreign proxies before any bytes are written.
func (c *Context) CheckProxy(proxy Proxy) error {
	if c.closed.Load() {
		return errors.New("context is closed")
	}
	if proxy == nil || proxy.ID() == 0 || proxy.Context() != c {
		return errors.New("invalid proxy")
	}
	registered, ok := c.proxies.Load(proxy.ID())
	if !ok || registered != proxy {
		return errors.New("proxy is not registered")
	}
	return nil
}

// Request describes one protocol request for Context.Request.
type Request struct {
	Proxy      Proxy
	Opcode     uint32
	Name       string // interface.request, used in errors
	Since      uint32 // version that introduced the request; 0 or 1 means always
	Destructor bool   // claim the proxy exactly once, as SendDestructor does
	Child      Proxy  // new_id object created by this request, or nil
	FDs        []int  // descriptors to attach; closed after a successful send
}

// Request runs one request with its whole lifecycle: it checks the proxy and
// version, allocates and registers Child (inheriting the parent's version),
// sends, and then either closes the sent descriptors or, on error,
// unregisters Child and leaves the descriptors with the caller. Child must
// already have this context; it is passed in args at its wire position.
func (c *Context) Request(r Request, args ...interface{}) error {
	if err := c.CheckProxy(r.Proxy); err != nil {
		return err
	}
	if v, ok := r.Proxy.(interface{ Version() uint32 }); ok {
		if err := CheckVersion(v.Version(), r.Since, r.Name); err != nil {
			return err
		}
	}
	if r.Child != nil {
		r.Child.SetID(c.AllocateID())
		if v, ok := r.Proxy.(interface{ Version() uint32 }); ok {
			if s, ok := r.Child.(interface{ SetVersion(uint32) }); ok {
				s.SetVersion(v.Version())
			}
		}
		c.Register(r.Child)
	}
	var err error
	if r.Destructor {
		err = c.SendDestructorWithFDs(r.Proxy, r.Opcode, r.FDs, args...)
	} else {
		err = c.SendRequestWithFDs(r.Proxy, r.Opcode, r.FDs, args...)
	}
	if err != nil {
		if r.Child != nil {
			c.Unregister(r.Child)
		}
		return err
	}
	for _, fd := range r.FDs {
		_ = CloseSentFD(fd)
	}
	return nil
}

// SendDestructor sends a destructor request exactly once. The proxy is
// claimed (unregistered) under the connection send lock, after marshaling
// and immediately before the write: concurrent destructors and later requests
// on the proxy fail without writing. A marshaling error leaves the proxy
// registered. A write error after the claim leaves the proxy unregistered;
// such an error means the connection is broken and must be closed.
func (c *Context) SendDestructor(proxy Proxy, opcode uint32, args ...interface{}) error {
	return c.SendDestructorWithFDs(proxy, opcode, nil, args...)
}

// SendDestructorWithFDs is SendDestructor for requests carrying FDs. On error
// the FDs remain owned by the caller.
func (c *Context) SendDestructorWithFDs(proxy Proxy, opcode uint32, fds []int, args ...interface{}) error {
	if err := c.CheckProxy(proxy); err != nil {
		return err
	}
	return c.display.sendRequest(proxy.ID(), uint16(opcode), fds, func() error { return c.claimDestroy(proxy) }, args)
}

func (c *Context) claimDestroy(proxy Proxy) error {
	if err := c.CheckProxy(proxy); err != nil {
		return err
	}
	if !c.proxies.CompareAndDelete(proxy.ID(), proxy) {
		return errors.New("proxy is not registered")
	}
	// The compositor may already have sent events for this object. Keep a
	// zombie until delete_id so those events are dropped, not fatal.
	c.display.objects.CompareAndSwap(proxy.ID(), proxy, &zombie{object: proxy})
	return nil
}

// zombie stands in for an object the client destroyed until the compositor
// acknowledges the destruction with wl_display.delete_id. Events that were in
// flight for it are discarded; their descriptors are closed.
type zombie struct{ object Object }

func (z *zombie) ID() uint32 { return z.object.ID() }

// Register registers a proxy object
func (c *Context) Register(proxy Proxy) {
	if proxy != nil && proxy.ID() != 0 && !c.closed.Load() {
		if proxy.Context() != c {
			return
		}
		c.proxies.Store(proxy.ID(), proxy)
		c.display.objects.Store(proxy.ID(), proxy)
	}
}

// Unregister removes a proxy object
// Only this proxy is removed: a zombie or a newer object that took the ID
// is left in place.
func (c *Context) Unregister(proxy Proxy) {
	if proxy != nil {
		c.proxies.CompareAndDelete(proxy.ID(), proxy)
		c.display.objects.CompareAndDelete(proxy.ID(), proxy)
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

// Version returns the protocol version the object was bound or created at,
// or 0 when it is unknown (for example after Registry.BindID).
func (p *BaseProxy) Version() uint32 {
	return p.version
}

// SetVersion records the object's protocol version. Registry.Bind sets it for
// globals and generated requests copy it from parent to child.
func (p *BaseProxy) SetVersion(v uint32) {
	p.version = v
}

// ErrVersionTooLow reports a request the bound object version does not have.
var ErrVersionTooLow = errors.New("wlturbo: request needs a newer object version")

// CheckVersion rejects a request introduced in version since when the
// object's known version is lower. An unknown version (0) is not checked.
func CheckVersion(version, since uint32, request string) error {
	if version != 0 && version < since {
		return fmt.Errorf("%w: %s needs version %d, object has %d", ErrVersionTooLow, request, since, version)
	}
	return nil
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

// OwnedFD owns one received descriptor. Close is idempotent; Take transfers
// ownership to the caller (who must close it). Descriptor zero is valid.
type OwnedFD struct {
	mu sync.Mutex
	fd int
}

func (f *OwnedFD) Take() (int, error) {
	if f == nil {
		return -1, errors.New("missing descriptor")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fd < 0 {
		return -1, errors.New("descriptor already taken or closed")
	}
	n := f.fd
	f.fd = -1
	return n, nil
}
func (f *OwnedFD) Close() error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fd < 0 {
		return nil
	}
	n := f.fd
	f.fd = -1
	return unix.Close(n)
}

// FD transfers ownership of the next descriptor assigned to this event.
// An unclaimed descriptor is closed when dispatch completes.
func (e *Event) FD() *OwnedFD {
	if e.fdIndex >= len(e.fds) {
		return nil
	}
	fd := e.fds[e.fdIndex]
	e.fds[e.fdIndex] = nil
	e.fdIndex++
	return fd
}

// Fd is the legacy raw descriptor API; prefer FD for explicit ownership.
func (e *Event) Fd() uintptr {
	fd := e.FD()
	if fd == nil {
		_ = e.Uint32()
		return 0
	}
	n, _ := fd.Take()
	return uintptr(n)
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
	if handler == nil {
		return
	}
	r.AddHandler("*", func(registry *Registry, name uint32, version uint32) {
		if global, ok := r.FindGlobalByName(name); ok {
			handler.HandleRegistryGlobal(RegistryGlobalEvent{Registry: r, Name: name, Interface: global.Interface, Version: version})
		}
	})
}

// AddGlobalRemoveHandler registers a handler for registry removals.
func (r *Registry) AddGlobalRemoveHandler(handler RegistryGlobalRemoveHandler) {
	if handler == nil {
		return
	}
	r.mu.Lock()
	r.removeHandlers = append(r.removeHandlers, handler)
	r.mu.Unlock()
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

	// Store before sending: the compositor may reply immediately.
	d.context.Register(callback)
	if err := d.SendRequest(1, 0, callbackID); err != nil {
		d.context.Unregister(callback)
		return nil, err
	}

	return callback, nil
}
