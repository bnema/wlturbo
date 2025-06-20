//go:build linux
// +build linux

package wlturbo

import (
	"fmt"
	"net"
	"golang.org/x/sys/unix"
	"sync"
	"sync/atomic"
	"syscall"
)

// fdQueue is a lock-free multi-producer single-consumer queue for file descriptors
type fdQueue struct {
	items [256]atomic.Pointer[fdItem]
	head  atomic.Uint64
	tail  atomic.Uint64
}

type fdItem struct {
	fd   int
	next atomic.Pointer[fdItem]
}

var (
	// Global FD queue for zero-allocation FD passing
	globalFDQueue = &fdQueue{}
	
	// Pre-allocated FD items pool
	fdItemPool = sync.Pool{
		New: func() interface{} {
			return &fdItem{}
		},
	}
	
	// Pre-allocated buffers for control messages
	controlBufferPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, unix.CmsgSpace(4*4)) // Space for 4 FDs
		},
	}
)

// enqueueFD adds a file descriptor to the lock-free queue
func (q *fdQueue) enqueueFD(fd int) {
	item := fdItemPool.Get().(*fdItem)
	item.fd = fd
	item.next.Store(nil)
	
	for {
		tail := q.tail.Load()
		next := tail & 255
		
		if q.items[next].CompareAndSwap(nil, item) {
			q.tail.Add(1)
			return
		}
		// Retry if CAS failed
	}
}

// dequeueFD removes and returns a file descriptor from the queue
func (q *fdQueue) dequeueFD() (int, bool) {
	for {
		head := q.head.Load()
		tail := q.tail.Load()
		
		if head >= tail {
			return -1, false
		}
		
		next := head & 255
		item := q.items[next].Load()
		
		if item == nil {
			continue
		}
		
		if q.head.CompareAndSwap(head, head+1) {
			fd := item.fd
			q.items[next].Store(nil)
			fdItemPool.Put(item)
			return fd, true
		}
	}
}

// recvmsgWithFDs receives a message potentially containing file descriptors
func (d *Display) recvmsgWithFDs(buf []byte) (n int, fds []int, err error) {
	// Get control buffer from pool
	oob := controlBufferPool.Get().([]byte)
	defer controlBufferPool.Put(oob)
	
	// Receive message with control data
	n, oobn, _, _, err := d.conn.(*net.UnixConn).ReadMsgUnix(buf, oob)
	if err != nil {
		return 0, nil, err
	}
	
	// Parse control messages if any
	if oobn > 0 {
		scms, err := syscall.ParseSocketControlMessage(oob[:oobn])
		if err != nil {
			return n, nil, fmt.Errorf("parse control message: %w", err)
		}
		
		for _, scm := range scms {
			if scm.Header.Type == syscall.SCM_RIGHTS {
				// Parse unix rights (file descriptors)
				parsedFDs, err := syscall.ParseUnixRights(&scm)
				if err != nil {
					return n, nil, fmt.Errorf("parse unix rights: %w", err)
				}
				
				// Add FDs to our lock-free queue
				for _, fd := range parsedFDs {
					globalFDQueue.enqueueFD(fd)
				}
				
				fds = append(fds, parsedFDs...)
			}
		}
	}
	
	return n, fds, nil
}

// sendmsgWithFDs sends a message potentially containing file descriptors
func (d *Display) sendmsgWithFDs(buf []byte, fds []int) error {
	d.sendMu.Lock()
	defer d.sendMu.Unlock()
	
	if len(fds) == 0 {
		// Fast path: no FDs to send
		_, err := d.conn.Write(buf)
		return err
	}
	
	// Create control message with file descriptors
	oob := syscall.UnixRights(fds...)
	
	// Send message with control data
	_, _, err := d.conn.(*net.UnixConn).WriteMsgUnix(buf, oob, nil)
	return err
}

// GetNextFD retrieves the next file descriptor from the queue
func GetNextFD() (int, bool) {
	return globalFDQueue.dequeueFD()
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