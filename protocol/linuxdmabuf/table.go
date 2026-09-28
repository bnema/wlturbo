package linuxdmabuf

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/bnema/wlturbo/wl"
	"golang.org/x/sys/unix"
)

// FormatEntry is one 16-byte format-table record. Padding is ignored.
type FormatEntry struct {
	Format   uint32
	Modifier uint64
}

// ReadFormatTable consumes an owned format_table descriptor, even on error.
// It reads a bounded snapshot with pread rather than mapping compositor-owned
// memory, avoiding SIGBUS if the backing file is unexpectedly truncated.
func ReadFormatTable(fd *wl.OwnedFD, size uint32) ([]FormatEntry, error) {
	if fd == nil {
		return nil, fmt.Errorf("missing format table descriptor")
	}
	n, err := fd.Take()
	if err != nil {
		return nil, err
	}
	defer unix.Close(n)
	if size%16 != 0 || size > 16<<20 {
		return nil, fmt.Errorf("invalid format table size %d", size)
	}
	b := make([]byte, size)
	for off := 0; off < len(b); {
		read, e := unix.Pread(n, b[off:], int64(off))
		off += read
		if e == unix.EINTR {
			continue
		}
		if e != nil {
			return nil, e
		}
		if read == 0 {
			return nil, io.ErrUnexpectedEOF
		}
	}
	entries := make([]FormatEntry, size/16)
	for i := range entries {
		off := i * 16
		entries[i] = FormatEntry{binary.NativeEndian.Uint32(b[off:]), binary.NativeEndian.Uint64(b[off+8:])}
	}
	return entries, nil
}

// TrancheIndices decodes the uint16 indices into a format table.
func TrancheIndices(data []byte, count int) ([]uint16, error) {
	if len(data)%2 != 0 {
		return nil, fmt.Errorf("odd tranche indices length")
	}
	indices := make([]uint16, len(data)/2)
	for i := range indices {
		indices[i] = binary.NativeEndian.Uint16(data[i*2:])
		if int(indices[i]) >= count {
			return nil, fmt.Errorf("format table index %d out of range", indices[i])
		}
	}
	return indices, nil
}
