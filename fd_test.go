//go:build linux
// +build linux

package wlturbo

import (
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// fdRecordingProxy consumes the file descriptor of every event it receives.
// Events without a descriptor are left untouched.
type fdRecordingProxy struct {
	BaseProxy
	fds *[]int
}

func (p *fdRecordingProxy) Dispatch(event *Event) {
	if fd := event.Fd(); fd != 0 {
		*p.fds = append(*p.fds, int(fd))
		return
	}
	// No descriptor arrived. Fd already consumed the placeholder argument.
}

// fdIgnoringProxy never reads the descriptor of an event.
type fdIgnoringProxy struct {
	BaseProxy
}

func (p *fdIgnoringProxy) Dispatch(*Event) {}

// unixSocketPair returns a connected socket pair as net.UnixConns.
func unixSocketPair(t *testing.T) (client, peer *net.UnixConn) {
	t.Helper()

	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}

	clientFile := os.NewFile(uintptr(fds[0]), "client")
	peerFile := os.NewFile(uintptr(fds[1]), "peer")

	clientConn, err := net.FileConn(clientFile)
	clientFile.Close()
	if err != nil {
		peerFile.Close()
		t.Fatalf("net.FileConn(client): %v", err)
	}

	peerConn, err := net.FileConn(peerFile)
	peerFile.Close()
	if err != nil {
		clientConn.Close()
		t.Fatalf("net.FileConn(peer): %v", err)
	}

	t.Cleanup(func() {
		clientConn.Close()
		peerConn.Close()
	})

	return clientConn.(*net.UnixConn), peerConn.(*net.UnixConn)
}

// sendMessageWithFDs writes one framed message and attaches fds to it.
func sendMessageWithFDs(t *testing.T, peer *net.UnixConn, msg []byte, fds []int) {
	t.Helper()

	var oob []byte
	if len(fds) > 0 {
		oob = unix.UnixRights(fds...)
	}
	if _, _, err := peer.WriteMsgUnix(msg, oob, nil); err != nil {
		t.Fatalf("WriteMsgUnix: %v", err)
	}
}

// newFDDisplay builds a display over the client end with a proxy registered at
// object ID 7 and returns the descriptors that proxy consumes.
func newFDDisplay(t *testing.T, client *net.UnixConn) (*Display, *[]int) {
	t.Helper()

	d := newDisplay(client)
	fds := []int{}
	d.objects.Store(uint32(7), &fdRecordingProxy{
		BaseProxy: BaseProxy{id: 7, context: d.context},
		fds:       &fds,
	})
	return d, &fds
}

// writeSentinel writes a unique byte string through fd and closes the caller's
// copy of the descriptor. Reading the same sentinel from the paired read end
// proves the descriptor really is the expected one.
func writeSentinel(t *testing.T, fd uintptr, sentinel string) {
	t.Helper()
	if _, err := unix.Write(int(fd), []byte(sentinel)); err != nil {
		t.Fatalf("write sentinel through received fd: %v", err)
	}
	if err := unix.Close(int(fd)); err != nil {
		t.Fatalf("close received fd: %v", err)
	}
}

// readSentinel reads exactly len(sentinel) bytes and compares them.
func readSentinel(t *testing.T, r *os.File, sentinel string) {
	t.Helper()
	buf := make([]byte, len(sentinel))
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if string(buf) != sentinel {
		t.Fatalf("sentinel = %q, want %q", buf, sentinel)
	}
}

func TestDisplayFD_Single(t *testing.T) {
	client, peer := unixSocketPair(t)
	d, received := newFDDisplay(t, client)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()

	sendMessageWithFDs(t, peer, message(7, 3, []byte{0, 0, 0, 0}), []int{int(w.Fd())})
	w.Close()

	if err := d.Dispatch(); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(*received) != 1 {
		t.Fatalf("received %d descriptors, want 1", len(*received))
	}

	writeSentinel(t, uintptr((*received)[0]), "single-fd-sentinel")
	readSentinel(t, r, "single-fd-sentinel")
}

func TestDisplayFD_MultipleInOneControlMessage(t *testing.T) {
	client, peer := unixSocketPair(t)
	d, received := newFDDisplay(t, client)

	firstR, firstW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer firstR.Close()
	secondR, secondW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer secondR.Close()

	sendMessageWithFDs(t, peer, message(7, 3, []byte{0, 0, 0, 0}),
		[]int{int(firstW.Fd()), int(secondW.Fd())})
	firstW.Close()
	secondW.Close()

	if err := d.Dispatch(); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	// A handler consumes one descriptor per fd argument, so the test drains
	// whatever the connection still holds, in arrival order.
	for {
		fd, ok := d.nextFD()
		if !ok {
			break
		}
		*received = append(*received, fd)
	}

	if len(*received) != 2 {
		t.Fatalf("received %d descriptors, want 2", len(*received))
	}

	writeSentinel(t, uintptr((*received)[0]), "first-pipe")
	writeSentinel(t, uintptr((*received)[1]), "second-pipe")

	readSentinel(t, firstR, "first-pipe")
	readSentinel(t, secondR, "second-pipe")
}

func TestDisplayFD_EventWithoutFDsAfterOneWithFDs(t *testing.T) {
	client, peer := unixSocketPair(t)
	d, received := newFDDisplay(t, client)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()

	// First event carries a descriptor.
	sendMessageWithFDs(t, peer, message(7, 3, []byte{0, 0, 0, 0}), []int{int(w.Fd())})
	w.Close()
	if err := d.Dispatch(); err != nil {
		t.Fatalf("first Dispatch: %v", err)
	}

	// Second event carries none and must not steal the first one.
	sendMessageWithFDs(t, peer, message(7, 4, []byte{0, 0, 0, 0}), nil)
	if err := d.Dispatch(); err != nil {
		t.Fatalf("second Dispatch: %v", err)
	}

	if len(*received) != 1 {
		t.Fatalf("received %d descriptors, want exactly 1", len(*received))
	}

	writeSentinel(t, uintptr((*received)[0]), "only-fd")
	readSentinel(t, r, "only-fd")
}

func TestDisplayFD_TwoDisplaysAreIsolated(t *testing.T) {
	firstClient, firstPeer := unixSocketPair(t)
	secondClient, secondPeer := unixSocketPair(t)

	firstDisplay, firstReceived := newFDDisplay(t, firstClient)
	secondDisplay, secondReceived := newFDDisplay(t, secondClient)

	firstR, firstW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer firstR.Close()
	secondR, secondW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer secondR.Close()

	sendMessageWithFDs(t, firstPeer, message(7, 3, []byte{0, 0, 0, 0}), []int{int(firstW.Fd())})
	firstW.Close()
	sendMessageWithFDs(t, secondPeer, message(7, 3, []byte{0, 0, 0, 0}), []int{int(secondW.Fd())})
	secondW.Close()

	if err := secondDisplay.Dispatch(); err != nil {
		t.Fatalf("second Dispatch: %v", err)
	}
	if err := firstDisplay.Dispatch(); err != nil {
		t.Fatalf("first Dispatch: %v", err)
	}

	// Each connection must only see its own descriptor.
	if len(*firstReceived) != 1 {
		t.Fatalf("first display received %d descriptors, want 1", len(*firstReceived))
	}
	if len(*secondReceived) != 1 {
		t.Fatalf("second display received %d descriptors, want 1", len(*secondReceived))
	}

	writeSentinel(t, uintptr((*secondReceived)[0]), "second-display")
	writeSentinel(t, uintptr((*firstReceived)[0]), "first-display")

	readSentinel(t, secondR, "second-display")
	readSentinel(t, firstR, "first-display")
}

func TestDisplayFD_TruncatedControlData(t *testing.T) {
	client, peer := unixSocketPair(t)
	d, _ := newFDDisplay(t, client)

	// One more descriptor than a single read can carry.
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	defer devNull.Close()

	fds := make([]int, 0, maxRecvFDs+1)
	for i := 0; i < maxRecvFDs+1; i++ {
		dup, err := unix.Dup(int(devNull.Fd()))
		if err != nil {
			t.Fatalf("dup: %v", err)
		}
		t.Cleanup(func() { _ = unix.Close(dup) })
		fds = append(fds, dup)
	}

	sendMessageWithFDs(t, peer, message(7, 3, []byte{0, 0, 0, 0}), fds)

	err = d.Dispatch()
	if !errors.Is(err, ErrMalformedFrame) {
		t.Fatalf("Dispatch error = %v, want %v", err, ErrMalformedFrame)
	}

	var perr *ProtocolError
	if !errors.As(err, &perr) {
		t.Fatalf("Dispatch error type = %T, want *ProtocolError", err)
	}
	if perr.Kind != "control_truncated" {
		t.Fatalf("error kind = %q, want %q", perr.Kind, "control_truncated")
	}

	// A truncated control message must not leave descriptors queued.
	d.fdMu.Lock()
	pending := len(d.pendingFDs)
	d.fdMu.Unlock()
	if pending != 0 {
		t.Fatalf("%d descriptors still queued after a truncated control message", pending)
	}
}

func TestDisplayFD_UnconsumedDescriptorClosedOnShutdown(t *testing.T) {
	client, peer := unixSocketPair(t)
	d := newDisplay(client)
	d.objects.Store(uint32(7), &fdIgnoringProxy{
		BaseProxy: BaseProxy{id: 7, context: d.context},
	})

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()

	sendMessageWithFDs(t, peer, message(7, 3, []byte{0, 0, 0, 0}), []int{int(w.Fd())})
	// Drop our own copy: the display now holds the only writer.
	w.Close()

	if err := d.Dispatch(); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	// While the display still holds the descriptor the pipe stays open.
	if err := r.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if _, err := r.Read(make([]byte, 1)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("read with the display holding the descriptor = %v, want %v", err, os.ErrDeadlineExceeded)
	}

	// Closing the display must release it, so the read end sees EOF.
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := r.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if _, err := r.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("read after Close = %v, want %v", err, io.EOF)
	}
}

// rawSocketPair returns a connected socket pair without registering cleanup,
// so loop tests can close descriptors deterministically.
func rawSocketPair(t *testing.T) (client, peer *net.UnixConn) {
	t.Helper()

	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}

	clientFile := os.NewFile(uintptr(fds[0]), "client")
	clientConn, err := net.FileConn(clientFile)
	clientFile.Close()
	if err != nil {
		t.Fatalf("net.FileConn(client): %v", err)
	}

	peerFile := os.NewFile(uintptr(fds[1]), "peer")
	peerConn, err := net.FileConn(peerFile)
	peerFile.Close()
	if err != nil {
		t.Fatalf("net.FileConn(peer): %v", err)
	}

	return clientConn.(*net.UnixConn), peerConn.(*net.UnixConn)
}

// openFDs reports how many descriptors the process holds.
func openFDs(t *testing.T) int {
	t.Helper()

	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("cannot count open descriptors: %v", err)
	}
	return len(entries)
}

func TestDisplayFD_NoLeakAcrossConnections(t *testing.T) {
	const iterations = 100

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	defer devNull.Close()

	// Warm up so caches and pools are populated before measuring.
	runFDLeakIteration(t, devNull)

	before := openFDs(t)
	for i := 0; i < iterations; i++ {
		runFDLeakIteration(t, devNull)
	}
	after := openFDs(t)

	if after > before {
		t.Fatalf("descriptor count grew from %d to %d over %d connections", before, after, iterations)
	}
}

// runFDLeakIteration opens a connection, delivers a descriptor nobody reads and
// tears everything down.
func runFDLeakIteration(t *testing.T, devNull *os.File) {
	t.Helper()

	client, peer := rawSocketPair(t)
	d := newDisplay(client)
	d.objects.Store(uint32(7), &fdIgnoringProxy{
		BaseProxy: BaseProxy{id: 7, context: d.context},
	})

	sent, err := unix.Dup(int(devNull.Fd()))
	if err != nil {
		t.Fatalf("dup: %v", err)
	}
	sendMessageWithFDs(t, peer, message(7, 3, []byte{0, 0, 0, 0}), []int{sent})
	if err := unix.Close(sent); err != nil {
		t.Fatalf("close sent descriptor: %v", err)
	}

	if err := d.Dispatch(); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := peer.Close(); err != nil {
		t.Fatalf("close peer: %v", err)
	}
}

func TestDisplayFD_ParseFailureClosesQueuedDescriptor(t *testing.T) {
	client, peer := unixSocketPair(t)
	d := newDisplay(client)
	d.objects.Store(uint32(7), &fdIgnoringProxy{
		BaseProxy: BaseProxy{id: 7, context: d.context},
	})

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()

	// A descriptor arrives together with a malformed frame.
	sendMessageWithFDs(t, peer, header(7, 3, 0), []int{int(w.Fd())})
	w.Close()

	err = d.Dispatch()
	if !errors.Is(err, ErrMalformedFrame) {
		t.Fatalf("Dispatch error = %v, want %v", err, ErrMalformedFrame)
	}

	d.fdMu.Lock()
	pending := len(d.pendingFDs)
	d.fdMu.Unlock()
	if pending != 0 {
		t.Fatalf("%d descriptors still queued after a malformed frame", pending)
	}

	if err := r.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if _, err := r.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("read after malformed frame = %v, want %v", err, io.EOF)
	}
}
