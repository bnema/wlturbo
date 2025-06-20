package wlturbo

import (
	"fmt"
	"golang.org/x/sys/unix"
)

// ShmPool represents a shared memory pool
type ShmPool struct {
	id     uint32
	fd     int
	size   int
	data   []byte
	offset int
}

// CreateShmPool creates a new shared memory pool
func CreateShmPool(size int) (*ShmPool, error) {
	// Create anonymous file
	fd, err := CreateAnonymousFile(int64(size))
	if err != nil {
		return nil, fmt.Errorf("failed to create anonymous file: %w", err)
	}
	
	// Map the file into memory
	data, err := MapMemory(fd, size)
	if err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("failed to map memory: %w", err)
	}
	
	return &ShmPool{
		fd:   fd,
		size: size,
		data: data,
	}, nil
}

// Close closes the shared memory pool
func (p *ShmPool) Close() error {
	if p.data != nil {
		if err := UnmapMemory(p.data); err != nil {
			return err
		}
		p.data = nil
	}
	
	if p.fd >= 0 {
		if err := unix.Close(p.fd); err != nil {
			return err
		}
		p.fd = -1
	}
	
	return nil
}

// Data returns the memory-mapped data
func (p *ShmPool) Data() []byte {
	return p.data
}

// FD returns the file descriptor
func (p *ShmPool) FD() int {
	return p.fd
}

// Size returns the pool size
func (p *ShmPool) Size() int {
	return p.size
}

// ShmBuffer represents a buffer allocated from a pool
type ShmBuffer struct {
	pool   *ShmPool
	offset int
	width  int
	height int
	stride int
	format uint32
}

// AllocateBuffer allocates a buffer from the pool
func (p *ShmPool) AllocateBuffer(width, height, stride int, format uint32) (*ShmBuffer, error) {
	size := height * stride
	
	if p.offset+size > p.size {
		return nil, fmt.Errorf("insufficient space in pool: need %d, have %d", size, p.size-p.offset)
	}
	
	buffer := &ShmBuffer{
		pool:   p,
		offset: p.offset,
		width:  width,
		height: height,
		stride: stride,
		format: format,
	}
	
	p.offset += size
	
	// Align to 64-byte boundary for cache efficiency
	padding := (64 - (p.offset % 64)) % 64
	p.offset += padding
	
	return buffer, nil
}

// Data returns the buffer's data slice
func (b *ShmBuffer) Data() []byte {
	size := b.height * b.stride
	return b.pool.data[b.offset : b.offset+size]
}

// Offset returns the buffer's offset in the pool
func (b *ShmBuffer) Offset() int {
	return b.offset
}

// Wayland pixel formats
const (
	// 32-bit formats
	FormatARGB8888 = 0
	FormatXRGB8888 = 1
	
	// 24-bit formats  
	FormatRGB888 = 0x34324752 // 'RG24'
	FormatBGR888 = 0x34324742 // 'BG24'
	
	// 16-bit formats
	FormatRGB565   = 0x36314752 // 'RG16'
	FormatXRGB1555 = 0x35315258 // 'XR15'
	
	// 8-bit formats
	FormatY8 = 0x20203859 // 'Y8  '
)