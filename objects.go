package wlturbo

import "sync"

// objectTable maps object IDs to objects. Unlike sync.Map, storing an ID that
// was deleted earlier reuses the map's storage, so the steady-state cycle of
// server-created objects (wl_callback per frame, recycled IDs) does not
// allocate.
type objectTable[V comparable] struct {
	mu sync.RWMutex
	m  map[uint32]V
}

func (t *objectTable[V]) Load(id uint32) (V, bool) {
	t.mu.RLock()
	v, ok := t.m[id]
	t.mu.RUnlock()
	return v, ok
}

func (t *objectTable[V]) Store(id uint32, v V) {
	t.mu.Lock()
	if t.m == nil {
		t.m = make(map[uint32]V)
	}
	t.m[id] = v
	t.mu.Unlock()
}

func (t *objectTable[V]) Delete(id uint32) {
	t.mu.Lock()
	delete(t.m, id)
	t.mu.Unlock()
}

// CompareAndDelete deletes id only while it maps to old.
func (t *objectTable[V]) CompareAndDelete(id uint32, old V) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if v, ok := t.m[id]; !ok || v != old {
		return false
	}
	delete(t.m, id)
	return true
}

// CompareAndSwap replaces id's value only while it maps to old.
func (t *objectTable[V]) CompareAndSwap(id uint32, old, new V) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if v, ok := t.m[id]; !ok || v != old {
		return false
	}
	t.m[id] = new
	return true
}
