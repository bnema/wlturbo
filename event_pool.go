//go:build linux
// +build linux

package wlturbo

import (
	"sync"
	"sync/atomic"
)

// Event pool for zero-allocation event handling
var eventPool = sync.Pool{
	New: func() interface{} {
		return &Event{
			data: make([]byte, 0, 4096), // Pre-allocate 4KB
		}
	},
}

// EventHandler is a function type for handling events with zero allocations
type EventHandler func(event *Event)

// EventDispatcher handles high-performance event dispatching
type EventDispatcher struct {
	// Lock-free handler lookup using atomic operations
	handlers [1024]atomic.Pointer[handlerEntry] // Fixed-size array for object IDs 0-1023
	
	// Extended handlers for object IDs >= 1024 (rare case)
	extHandlers sync.Map
	
	// Pre-allocated event objects to avoid allocations
	eventCache [64]Event
	eventIndex atomic.Uint32
}

// handlerEntry stores handlers for a specific object
type handlerEntry struct {
	// Array of handlers indexed by opcode (most objects have < 16 opcodes)
	handlers [32]EventHandler
	
	// Extended handlers for opcodes >= 32 (very rare)
	extHandlers *sync.Map
}

// NewEventDispatcher creates a high-performance event dispatcher
func NewEventDispatcher() *EventDispatcher {
	return &EventDispatcher{}
}

// RegisterHandler registers an event handler with zero allocation in common case
func (d *EventDispatcher) RegisterHandler(objectID uint32, opcode uint16, handler EventHandler) {
	if objectID < 1024 {
		// Fast path: use lock-free array
		for {
			entry := d.handlers[objectID].Load()
			if entry == nil {
				// Create new entry
				newEntry := &handlerEntry{}
				if opcode < 32 {
					newEntry.handlers[opcode] = handler
				} else {
					newEntry.extHandlers = &sync.Map{}
					newEntry.extHandlers.Store(opcode, handler)
				}
				
				// Try to set atomically
				if d.handlers[objectID].CompareAndSwap(nil, newEntry) {
					return
				}
				// Retry if someone else created it
				continue
			}
			
			// Entry exists, update it
			if opcode < 32 {
				// Direct array access (no locking needed for write)
				entry.handlers[opcode] = handler
			} else {
				// Extended handlers
				if entry.extHandlers == nil {
					entry.extHandlers = &sync.Map{}
				}
				entry.extHandlers.Store(opcode, handler)
			}
			return
		}
	} else {
		// Slow path: use sync.Map for large object IDs
		entry, _ := d.extHandlers.LoadOrStore(objectID, &handlerEntry{})
		h := entry.(*handlerEntry)
		
		if opcode < 32 {
			h.handlers[opcode] = handler
		} else {
			if h.extHandlers == nil {
				h.extHandlers = &sync.Map{}
			}
			h.extHandlers.Store(opcode, handler)
		}
	}
}

// Dispatch dispatches an event with minimal overhead
//
//go:inline
func (d *EventDispatcher) Dispatch(objectID uint32, opcode uint16, data []byte) {
	var handler EventHandler
	
	// Fast lookup path
	if objectID < 1024 {
		entry := d.handlers[objectID].Load()
		if entry != nil {
			if opcode < 32 {
				handler = entry.handlers[opcode]
			} else if entry.extHandlers != nil {
				if h, ok := entry.extHandlers.Load(opcode); ok {
					handler = h.(EventHandler)
				}
			}
		}
	} else {
		// Slow path for large object IDs
		if entry, ok := d.extHandlers.Load(objectID); ok {
			h := entry.(*handlerEntry)
			if opcode < 32 {
				handler = h.handlers[opcode]
			} else if h.extHandlers != nil {
				if fn, ok := h.extHandlers.Load(opcode); ok {
					handler = fn.(EventHandler)
				}
			}
		}
	}
	
	if handler == nil {
		return
	}
	
	// Get event from pool
	event := eventPool.Get().(*Event)
	event.ProxyID = objectID
	event.Opcode = opcode
	event.data = append(event.data[:0], data...) // Reuse backing array
	event.offset = 0
	
	// Call handler
	handler(event)
	
	// Return to pool
	eventPool.Put(event)
}

// BatchDispatch processes multiple events in a batch for better cache locality
func (d *EventDispatcher) BatchDispatch(events []RawEvent) {
	// Process events in batches to improve cache performance
	for i := range events {
		e := &events[i]
		d.Dispatch(e.ObjectID, e.Opcode, e.Data)
	}
}

// RawEvent represents a raw event before dispatch
type RawEvent struct {
	ObjectID uint32
	Opcode   uint16
	Data     []byte
}

// DirectDispatcher provides the absolute fastest dispatch path for hot events
type DirectDispatcher struct {
	// Direct function pointers for the hottest paths (e.g., pointer motion)
	pointerMotion   func(surfaceX, surfaceY Fixed)
	pointerButton   func(button, state uint32)
	keyboardKey     func(key, state uint32)
	frameCallback   func(callbackData uint32)
	
	// Fallback to regular dispatcher
	fallback *EventDispatcher
}

// DispatchPointerMotion dispatches pointer motion events with zero overhead
//
//go:inline
func (d *DirectDispatcher) DispatchPointerMotion(surfaceX, surfaceY Fixed) {
	if d.pointerMotion != nil {
		d.pointerMotion(surfaceX, surfaceY)
	}
}

// DispatchPointerButton dispatches pointer button events with zero overhead
//
//go:inline
func (d *DirectDispatcher) DispatchPointerButton(button, state uint32) {
	if d.pointerButton != nil {
		d.pointerButton(button, state)
	}
}

// Ensure cache line alignment for hot data structures
type cacheLinePad [64]byte

// PerformanceStats tracks dispatcher performance
type PerformanceStats struct {
	EventsDispatched atomic.Uint64
	_                cacheLinePad
	NanosecondsSpent atomic.Uint64
	_                cacheLinePad
	CacheMisses      atomic.Uint64
	_                cacheLinePad
}

// GetAverageLatency returns average dispatch latency in nanoseconds
func (s *PerformanceStats) GetAverageLatency() uint64 {
	events := s.EventsDispatched.Load()
	if events == 0 {
		return 0
	}
	return s.NanosecondsSpent.Load() / events
}