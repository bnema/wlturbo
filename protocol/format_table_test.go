//go:build linux

package protocol_test

import (
	"errors"
	"github.com/bnema/wlturbo"
	"github.com/bnema/wlturbo/protocol/linuxdmabuf"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"testing"
)

// Exercise format_table through generated event dispatch so the received FD
// has real transport ownership; verify closing by inspecting the taken FD.
func TestMalformedFormatTables(t *testing.T) {
	for _, tc := range []struct {
		name     string
		contents []byte
		size     uint32
		wantErr  bool
	}{
		{"unaligned", make([]byte, 16), 17, true},
		{"truncated", make([]byte, 16), 32, true},
		{"zero", nil, 0, false},
		{"oversize", nil, (16 << 20) + 16, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, p := pair(t)
			d, e := wlturbo.ConnectFromConn(c)
			noerr(t, e)
			defer d.Close()
			request(t, p)
			fb := linuxdmabuf.NewLinuxDmabufFeedback(d.Context())
			fb.SetID(d.AllocateID())
			d.Context().Register(fb)
			file, e := os.CreateTemp(t.TempDir(), "table")
			noerr(t, e)
			defer file.Close()
			_, e = file.Write(tc.contents)
			noerr(t, e)
			called := false
			fb.OnFormatTable(func(fd *wlturbo.OwnedFD, size uint32) {
				called = true
				entries, err := linuxdmabuf.ReadFormatTable(fd, size)
				if (err != nil) != tc.wantErr {
					t.Fatalf("size=%d entries=%v err=%v", size, entries, err)
				}
				if tc.name == "truncated" && !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatalf("short read: %v", err)
				}
				// Take/Close must be idempotent, including after a failed size check.
				if _, err = fd.Take(); err == nil {
					t.Fatal("already consumed fd still takeable")
				}
				noerr(t, fd.Close())
				noerr(t, fd.Close())
			})
			send(t, p, msg(fb.ID(), 1, tc.size), int(file.Fd()))
			noerr(t, d.Dispatch())
			if !called {
				t.Fatal("no callback")
			}
			// The transport FD was a duplicate: caller's descriptor is still open.
			if _, e = unix.FcntlInt(file.Fd(), unix.F_GETFD, 0); e != nil {
				t.Fatalf("sender fd closed: %v", e)
			}
		})
	}
}
