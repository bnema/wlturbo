//go:build linux
// +build linux

package wlturbo

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// maxRecvFDs bounds how many ancillary file descriptors a single read accepts.
// A socket read that carries more than this is reported as a protocol failure
// instead of silently dropping descriptors.
const maxRecvFDs = 64

type controlBuffer struct{ data []byte }

// controlBufferPool provides control-message buffers sized for maxRecvFDs.
var controlBufferPool = sync.Pool{
	New: func() interface{} {
		return &controlBuffer{data: make([]byte, unix.CmsgSpace(maxRecvFDs*4))}
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

	buffer := controlBufferPool.Get().(*controlBuffer)
	defer controlBufferPool.Put(buffer)
	oob := buffer.data

	n, oobn, flags, _, err := d.unix.ReadMsgUnix(d.recvBuf, oob)
	if n < 0 {
		n = 0
	}

	if oobn > 0 {
		fds, perr := parseFDs(oob[:oobn])
		d.queueFDs(fds)
		if perr != nil {
			err = &ProtocolError{Kind: "malformed_control", Err: perr}
		}
	}

	if flags&unix.MSG_CTRUNC != 0 {
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
	var fds []int
	for len(oob) > 0 {
		if len(oob) < unix.SizeofCmsghdr {
			return fds, fmt.Errorf("short control header")
		}
		var hdr unix.Cmsghdr
		// Parse each header independently: a malformed later header must not
		// discard rights already decoded from earlier messages.
		if err := readControlHeader(oob, &hdr); err != nil {
			return fds, err
		}
		length := int(hdr.Len)
		if length < unix.SizeofCmsghdr || length > len(oob) {
			return fds, fmt.Errorf("invalid control length %d", length)
		}
		if hdr.Level == unix.SOL_SOCKET && hdr.Type == unix.SCM_RIGHTS {
			message := syscall.SocketControlMessage{Header: syscall.Cmsghdr{Len: uint64(length), Level: int32(hdr.Level), Type: int32(hdr.Type)}, Data: oob[unix.SizeofCmsghdr:length]}
			parsed, err := syscall.ParseUnixRights(&message)
			if err != nil {
				return fds, fmt.Errorf("parse unix rights: %w", err)
			}
			fds = append(fds, parsed...)
		}
		step := unix.CmsgSpace(length - unix.SizeofCmsghdr)
		if step > len(oob) {
			break
		} // final control message may omit trailing padding
		oob = oob[step:]
	}
	return fds, nil
}

func readControlHeader(data []byte, hdr *unix.Cmsghdr) error {
	// Linux cmsghdr has a native-endian size_t followed by two int32s.
	if len(data) < unix.SizeofCmsghdr {
		return fmt.Errorf("short control header")
	}
	*hdr = *(*unix.Cmsghdr)(unsafe.Pointer(&data[0]))
	return nil
}

// queueFDs is called only by the active Dispatch reader.
func (d *Display) queueFDs(fds []int) { d.pendingFDs = append(d.pendingFDs, fds...) }

// closePendingFDs is called under recvMu (including during shutdown).
func (d *Display) closePendingFDs() {
	fds := d.pendingFDs
	d.pendingFDs = nil
	for _, fd := range fds {
		if fd >= 0 {
			_ = unix.Close(fd)
		}
	}
}

// CloseSentFD releases a descriptor after a complete successful Wayland send.
func CloseSentFD(fd int) error { return unix.Close(fd) }

// sendmsgWithFDs sends a message, attaching file descriptors when present.
func (d *Display) sendmsgWithFDs(buf []byte, fds []int) error {
	d.sendMu.Lock()
	defer d.sendMu.Unlock()

	if d.closed.Load() {
		return net.ErrClosed
	}
	if len(fds) == 0 {
		n, err := d.conn.Write(buf)
		if err == nil && n != len(buf) {
			return io.ErrShortWrite
		}
		return err
	}
	if d.unix == nil {
		return errors.New("wlturbo: cannot send file descriptors over a non-Unix connection")
	}

	n, _, err := d.unix.WriteMsgUnix(buf, unix.UnixRights(fds...), nil)
	if err == nil && n != len(buf) {
		return io.ErrShortWrite
	}
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
