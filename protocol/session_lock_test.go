//go:build linux

package protocol_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/bnema/wlturbo"
	"github.com/bnema/wlturbo/protocol/core"
	"github.com/bnema/wlturbo/protocol/extsessionlock"
)

// These tests use ordinary generated proxies and the production transport on a
// socketpair. Only the server wire peer is scripted; no request/dispatch hooks
// are replaced, and no live compositor or session lock is used.
func TestSessionLockLockedLifetime(t *testing.T) {
	s := newWireEnv(t)
	ctx := s.d.Context()
	compositor := core.NewCompositor(ctx)
	s.bind(1, core.CompositorInterface, 6, 6, compositor)
	surface, err := compositor.CreateSurface()
	noerr(t, err)
	wantWords(t, "create_surface", s.req(compositor.ID(), 0), surface.ID())
	out := core.NewOutput(ctx)
	s.bind(2, core.OutputInterface, 4, 4, out)
	mgr := extsessionlock.NewExtSessionLockManager(ctx)
	s.bind(3, extsessionlock.ExtSessionLockManagerInterface, 1, 1, mgr)
	lock, err := mgr.Lock()
	noerr(t, err)
	wantWords(t, "lock", s.req(mgr.ID(), 1), lock.ID())
	if lock.Version() != 1 || lock.Context() != ctx {
		t.Fatalf("lock version=%d context=%p", lock.Version(), lock.Context())
	}
	noerr(t, ctx.CheckProxy(lock))

	// The manager destructor must not invalidate its lock children.
	noerr(t, mgr.Destroy())
	wantWords(t, "manager.destroy", s.req(mgr.ID(), 0))
	if ctx.CheckProxy(mgr) == nil {
		t.Fatal("destroyed manager remains registered")
	}
	s.event(1, 1, wordBytes(mgr.ID())...) // wl_display.delete_id
	noerr(t, ctx.CheckProxy(lock))
	ls, err := lock.GetLockSurface(surface, out)
	noerr(t, err)
	wantWords(t, "get_lock_surface", s.req(lock.ID(), 1), ls.ID(), surface.ID(), out.ID())
	if ls.Version() != 1 || ls.Context() != ctx {
		t.Fatalf("lock surface version=%d context=%p", ls.Version(), ls.Context())
	}
	noerr(t, ctx.CheckProxy(ls))

	var events []string
	lock.OnLocked(func() { events = append(events, "locked") })
	lock.OnFinished(func() { events = append(events, "finished") })
	var configures [][3]uint32
	ls.OnConfigure(func(serial, width, height uint32) {
		configures = append(configures, [3]uint32{serial, width, height})
		noerr(t, ls.AckConfigure(serial))
	})
	s.event(ls.ID(), 0, wordBytes(0x89abcdef, 1920, 1080)...)
	wantWords(t, "ack_configure", s.req(ls.ID(), 1), 0x89abcdef)
	s.event(lock.ID(), 0)
	s.event(lock.ID(), 1) // finished after locked still requires unlock_and_destroy
	if !reflect.DeepEqual(events, []string{"locked", "finished"}) ||
		!reflect.DeepEqual(configures, [][3]uint32{{0x89abcdef, 1920, 1080}}) {
		t.Fatalf("events=%v configures=%v", events, configures)
	}
	// Neither event is a destructor; the application must choose the request.
	noerr(t, ctx.CheckProxy(lock))
	noerr(t, lock.UnlockAndDestroy())
	wantWords(t, "unlock_and_destroy", s.req(lock.ID(), 2))
	if ctx.CheckProxy(lock) == nil {
		t.Fatal("unlocked lock remains registered")
	}
	s.event(1, 1, wordBytes(lock.ID())...)

	// Lock destruction also leaves its children and core objects valid.
	noerr(t, ctx.CheckProxy(ls))
	noerr(t, ctx.CheckProxy(surface))
	noerr(t, ctx.CheckProxy(out))
	s.event(ls.ID(), 0, wordBytes(0xffffffff, 800, 600)...)
	wantWords(t, "ack after parent destruction", s.req(ls.ID(), 1), 0xffffffff)
	if len(configures) != 2 || configures[1] != [3]uint32{0xffffffff, 800, 600} {
		t.Fatal(configures)
	}
	noerr(t, ls.Destroy())
	wantWords(t, "surface.destroy", s.req(ls.ID(), 0))
	if ctx.CheckProxy(ls) == nil {
		t.Fatal("destroyed lock surface remains registered")
	}
	// Events in flight on a destroyed object are dropped until delete_id.
	s.event(ls.ID(), 0, wordBytes(12, 640, 480)...)
	if len(configures) != 2 {
		t.Fatal("configure delivered to destroyed surface")
	}
	s.event(1, 1, wordBytes(ls.ID())...)
	if err := ls.AckConfigure(12); err == nil {
		t.Fatal("ack on destroyed surface succeeded")
	}
	if err := ls.Destroy(); err == nil {
		t.Fatal("second surface destructor succeeded")
	}
	if err := lock.Destroy(); err == nil {
		t.Fatal("second lock destructor succeeded")
	}
	if child, err := mgr.Lock(); err == nil || child != nil {
		t.Fatal("destroyed manager created a child", child, err)
	}
	if child, err := lock.GetLockSurface(surface, out); err == nil || child != nil {
		t.Fatal("destroyed lock created a child", child, err)
	}
	s.noRequest()
	noerr(t, surface.Destroy())
	wantWords(t, "wl_surface.destroy", s.req(surface.ID(), 0))
	noerr(t, out.Release())
	wantWords(t, "wl_output.release", s.req(out.ID(), 0))
}

func TestSessionLockDeniedDestroy(t *testing.T) {
	s := newWireEnv(t)
	mgr := extsessionlock.NewExtSessionLockManager(s.d.Context())
	s.bind(1, extsessionlock.ExtSessionLockManagerInterface, 1, 1, mgr)
	lock, err := mgr.Lock()
	noerr(t, err)
	wantWords(t, "lock", s.req(mgr.ID(), 1), lock.ID())
	var locked, finished bool
	lock.OnLocked(func() { locked = true })
	lock.OnFinished(func() { finished = true })
	s.event(lock.ID(), 1)
	if locked || !finished {
		t.Fatalf("locked=%t finished=%t", locked, finished)
	}
	noerr(t, s.d.Context().CheckProxy(lock))
	noerr(t, lock.Destroy()) // never locked: destroy, not unlock_and_destroy
	wantWords(t, "denied.destroy", s.req(lock.ID(), 0))
	if err := lock.UnlockAndDestroy(); err == nil {
		t.Fatal("second destructor succeeded")
	}
	s.noRequest()
	// Destroying one child must not invalidate the manager or its next child.
	noerr(t, s.d.Context().CheckProxy(mgr))
	next, err := mgr.Lock()
	noerr(t, err)
	wantWords(t, "next lock", s.req(mgr.ID(), 1), next.ID())
	noerr(t, next.Destroy())
	wantWords(t, "next.destroy", s.req(next.ID(), 0))
	noerr(t, mgr.Destroy())
	wantWords(t, "manager.destroy", s.req(mgr.ID(), 0))
}

func TestSessionLockConcurrentDestructors(t *testing.T) {
	s := newWireEnv(t)
	mgr := extsessionlock.NewExtSessionLockManager(s.d.Context())
	s.bind(1, extsessionlock.ExtSessionLockManagerInterface, 1, 1, mgr)
	lock, err := mgr.Lock()
	noerr(t, err)
	wantWords(t, "lock", s.req(mgr.ID(), 1), lock.ID())
	s.event(lock.ID(), 1)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); results <- lock.Destroy() }()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful destructors=%d, want 1", successes)
	}
	wantWords(t, "single destructor", s.req(lock.ID(), 0))
	s.noRequest()
}

func TestSessionLockMalformedEvents(t *testing.T) {
	for _, tc := range []struct {
		name    string
		surface bool
		op      uint16
		body    []byte
	}{
		{"locked trailing word", false, 0, wordBytes(1)},
		{"finished trailing word", false, 1, wordBytes(1)},
		{"configure missing height", true, 0, wordBytes(1, 800)},
		{"configure trailing word", true, 0, wordBytes(1, 800, 600, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newWireEnv(t)
			mgr := extsessionlock.NewExtSessionLockManager(s.d.Context())
			s.bind(1, extsessionlock.ExtSessionLockManagerInterface, 1, 1, mgr)
			lock, err := mgr.Lock()
			noerr(t, err)
			s.req(mgr.ID(), 1)
			id := lock.ID()
			called := false
			lock.OnLocked(func() { called = true })
			lock.OnFinished(func() { called = true })
			if tc.surface {
				ls, err := lock.GetLockSurface(s.surface(), s.output())
				noerr(t, err)
				s.req(lock.ID(), 1)
				ls.OnConfigure(func(_, _, _ uint32) { called = true })
				id = ls.ID()
			}
			send(t, s.p, payload(id, tc.op, tc.body...))
			if err := s.d.Dispatch(); !errors.Is(err, wlturbo.ErrMalformedFrame) {
				t.Fatalf("malformed event: %v", err)
			}
			if called {
				t.Fatal("malformed event reached a handler")
			}
		})
	}
}
