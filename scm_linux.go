//go:build linux
// +build linux

package wlturbo

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"sync"
	"syscall"
)

// maxRecvFDs bounds how many ancillary file descriptors a single read accepts.
// A socket read that carries more than this is reported as a protocol failure
// instead of silently dropping descriptors.
const maxRecvFDs = 64

// controlBufferPool provides control-message buffers sized for maxRecvFDs.
var controlBufferPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, unix.CmsgSpace(maxRecvFDs*4))
	},
}

// readChunk reads one chunk of bytes from the display connection and queues any
// ancillary file descriptors that arrive with it. A single read never defines a
// message boundary; callers frame the returned bytes themselves.
//
// Descriptors are owned by the connection and consumed in arrival order by
// Event.Fd, which is what the Wayland wire protocol requires: an fd belongs to
// the argument position in the byte stream, not to whichever message happens to
// end first.
func (d *Display) readChunk() (int, error) {
	if d.unix == nil {
		n, err := d.conn.Read(d.recvBuf)
		if n < 0 {
			n = 0
		}
		return n, err
	}

	oob := controlBufferPool.Get().([]byte)
	defer controlBufferPool.Put(oob)

	n, oobn, flags, _, err := d.unix.ReadMsgUnix(d.recvBuf, oob)
	if n < 0 {
		n = 0
	}

	if oobn > 0 {
		fds, perr := parseFDs(oob[:oobn])
		d.queueFDs(fds)
		if perr != nil && err == nil {
			err = perr
		}
	}

	if flags&unix.MSG_CTRUNC != 0 && err == nil {
		// Descriptors were dropped by the kernel, so the connection can no
		// longer be demarshalled correctly: treat it as a protocol failure so
		// the caller closes the descriptors that did arrive.
		err = &ProtocolError{Kind: "control_truncated", Err: ErrMalformedFrame}
	}
	return n, err
}

// parseFDs extracts the file descriptors carried by socket control messages.
// Descriptors parsed before an error are returned so the caller can close them.
func parseFDs(oob []byte) ([]int, error) {
	scms, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, fmt.Errorf("parse control message: %w", err)
	}

	var fds []int
	for i := range scms {
		scm := &scms[i]
		if scm.Header.Type != syscall.SCM_RIGHTS {
			continue
		}
		parsed, err := syscall.ParseUnixRights(scm)
		if err != nil {
			return fds, fmt.Errorf("parse unix rights: %w", err)
		}
		fds = append(fds, parsed...)
	}
	return fds, nil
}

// queueFDs appends descriptors to this connection's pending list.
func (d *Display) queueFDs(fds []int) {
	if len(fds) == 0 {
		return
	}
	d.fdMu.Lock()
	d.pendingFDs = append(d.pendingFDs, fds...)
	d.fdMu.Unlock()
}

// nextFD removes and returns the oldest pending descriptor.
func (d *Display) nextFD() (int, bool) {
	d.fdMu.Lock()
	defer d.fdMu.Unlock()
	if len(d.pendingFDs) == 0 {
		return -1, false
	}
	fd := d.pendingFDs[0]
	d.pendingFDs = d.pendingFDs[1:]
	return fd, true
}

// closePendingFDs closes every descriptor this connection still holds.
func (d *Display) closePendingFDs() {
	d.fdMu.Lock()
	fds := d.pendingFDs
	d.pendingFDs = nil
	d.fdMu.Unlock()

	for _, fd := range fds {
		if fd >= 0 {
			_ = unix.Close(fd)
		}
	}
}

// sendmsgWithFDs sends a message, attaching file descriptors when present.
func (d *Display) sendmsgWithFDs(buf []byte, fds []int) error {
	d.sendMu.Lock()
	defer d.sendMu.Unlock()

	if len(fds) == 0 {
		_, err := d.conn.Write(buf)
		return err
	}
	if d.unix == nil {
		return errors.New("wlturbo: cannot send file descriptors over a non-Unix connection")
	}

	_, _, err := d.unix.WriteMsgUnix(buf, unix.UnixRights(fds...), nil)
	return err
}

// Memory mapping helpers for shared memory buffers

// CreateAnonymousFile creates an anonymous file for shared memory
func CreateAnonymousFile(size int64) (fd int, err error) {
	// Try memfd_create first (Linux 3.17+)
	fd, err = unix.MemfdCreate("wlclient-shm", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err == nil {
		// Set file size
		err = unix.Ftruncate(fd, size)
		if err != nil {
			_ = unix.Close(fd)
			return -1, err
		}

		// Add seals to prevent resizing
		_, err = unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS,
			unix.F_SEAL_SHRINK|unix.F_SEAL_GROW|unix.F_SEAL_SEAL)
		if err != nil {
			_ = unix.Close(fd)
			return -1, err
		}

		return fd, nil
	}

	// Fallback to O_TMPFILE if available
	fd, err = unix.Open("/dev/shm", unix.O_TMPFILE|unix.O_RDWR|unix.O_CLOEXEC, 0600)
	if err == nil {
		err = unix.Ftruncate(fd, size)
		if err != nil {
			_ = unix.Close(fd)
			return -1, err
		}
		return fd, nil
	}

	// Final fallback: create temp file and unlink
	name := fmt.Sprintf("/dev/shm/wlclient-%d", unix.Getpid())
	fd, err = unix.Open(name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC, 0600)
	if err != nil {
		return -1, err
	}

	// Unlink immediately
	_ = unix.Unlink(name)

	// Set size
	err = unix.Ftruncate(fd, size)
	if err != nil {
		unix.Close(fd)
		return -1, err
	}

	return fd, nil
}

// MapMemory maps a file descriptor into memory
func MapMemory(fd int, size int) ([]byte, error) {
	return unix.Mmap(fd, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
}

// UnmapMemory unmaps memory
func UnmapMemory(data []byte) error {
	return unix.Munmap(data)
}
