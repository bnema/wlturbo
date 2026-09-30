package scanner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const requestArgsXML = `<protocol name="argsfix">
<interface name="args_obj" version="1">
 <request name="mixed">
  <arg name="serial" type="uint"/><arg name="dx" type="int"/><arg name="fx" type="fixed"/>
  <arg name="name" type="string"/><arg name="blob" type="array"/>
  <arg name="peer" type="object" interface="args_obj" allow-null="true"/>
 </request>
 <request name="make"><arg name="id" type="new_id" interface="args_obj"/><arg name="n" type="uint"/></request>
 <request name="give"><arg name="tag" type="string"/><arg name="fd" type="fd"/><arg name="size" type="uint"/></request>
 <request name="destroy" type="destructor"/>
</interface></protocol>`

func generateArgsFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "argsfix.xml")
	if err := os.WriteFile(path, []byte(requestArgsXML), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewScanner()
	if err := s.ParseXML(path); err != nil {
		t.Fatal(err)
	}
	b, err := s.Generate("argsfix")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Generated requests must use typed arguments only: no interface{} boxing
// path and no uintptr placeholder for descriptors.
func TestGeneratedRequestsUseTypedArgs(t *testing.T) {
	text := generateArgsFixture(t)
	for _, want := range []string{
		"RequestArgs(wl.Request{Proxy: o, Opcode: 0, Name: \"args_obj.mixed\"}, wl.ArgUint(serial), wl.ArgInt(dx), wl.ArgFixed(fx), wl.ArgString(name), wl.ArgArray(blob), wl.ArgObject(arg5))",
		"Child: child}, wl.ArgObject(child), wl.ArgUint(n))",
		"FDs: []int{fd}}, wl.ArgString(tag), wl.ArgFD(), wl.ArgUint(size))",
		"Destructor: true})",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("generated code missing %q\n%s", want, text)
		}
	}
	for _, bad := range []string{".Request(wl.Request", "uintptr("} {
		if strings.Contains(text, bad) {
			t.Errorf("generated code still contains %q", bad)
		}
	}
}

// The generated requests compile against the transport, put the exact wire
// bytes on the socket, and do not allocate for realistic numeric values.
func TestGeneratedRequestsWireAndAllocations(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go unavailable")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("args.go", generateArgsFixture(t))
	write("go.mod", "module example.com/argsfix\n\ngo 1.27\n\nrequire github.com/bnema/wlturbo v0.0.0\nreplace github.com/bnema/wlturbo => "+root+"\n")
	write("args_test.go", `package argsfix

import (
	"bytes"
	"net"
	"os"
	"testing"
	"time"

	"github.com/bnema/wlturbo"
	"golang.org/x/sys/unix"
)

type sink struct{ last []byte }

func (c *sink) Read([]byte) (int, error) { return 0, net.ErrClosed }
func (c *sink) Write(p []byte) (int, error) {
	c.last = append(c.last[:0], p...)
	return len(p), nil
}
func (c *sink) Close() error                     { return nil }
func (c *sink) LocalAddr() net.Addr              { return a{} }
func (c *sink) RemoteAddr() net.Addr             { return a{} }
func (c *sink) SetDeadline(time.Time) error      { return nil }
func (c *sink) SetReadDeadline(time.Time) error  { return nil }
func (c *sink) SetWriteDeadline(time.Time) error { return nil }

type a struct{}

func (a) Network() string { return "t" }
func (a) String() string  { return "t" }

func setup(t testing.TB) (*wlturbo.Display, *sink, *ArgsObj) {
	c := &sink{}
	d, err := wlturbo.ConnectFromConn(c)
	if err != nil {
		t.Fatal(err)
	}
	o := NewArgsObj(d.Context())
	o.SetID(d.AllocateID())
	d.Context().Register(o)
	return d, c, o
}

func TestMixedWire(t *testing.T) {
	_, c, o := setup(t)
	peer := NewArgsObj(o.Context())
	peer.SetID(0x1234)
	if err := o.Mixed(1000, -2, wlturbo.Fixed(512), "ab", []byte{9, 8, 7}, peer); err != nil {
		t.Fatal(err)
	}
	want := []byte{
		byte(o.ID()), 0, 0, 0, 0, 0, 40, 0,
		0xe8, 3, 0, 0,
		0xfe, 0xff, 0xff, 0xff,
		0, 2, 0, 0,
		3, 0, 0, 0, 'a', 'b', 0, 0,
		3, 0, 0, 0, 9, 8, 7, 0,
		0x34, 0x12, 0, 0,
	}
	if !bytes.Equal(c.last, want) {
		t.Fatalf("got %x want %x", c.last, want)
	}
	if err := o.Mixed(1, 2, 3, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := c.last[len(c.last)-4:]; !bytes.Equal(got, []byte{0, 0, 0, 0}) {
		t.Fatalf("nil object = %x", got)
	}
}

func TestMixedAllocFree(t *testing.T) {
	_, _, o := setup(t)
	blob := []byte("blob")
	n := testing.AllocsPerRun(200, func() {
		if err := o.Mixed(123456, -70000, wlturbo.Fixed(1<<20), "hello", blob, nil); err != nil {
			t.Fatal(err)
		}
	})
	if n != 0 {
		t.Fatalf("Mixed: %v allocs/op, want 0", n)
	}
}

func TestChildAndDestructor(t *testing.T) {
	d, c, o := setup(t)
	kid, err := o.Make(77777)
	if err != nil {
		t.Fatal(err)
	}
	if kid.ID() == 0 || c.last[8] != byte(kid.ID()) {
		t.Fatalf("child id %d not on the wire: %x", kid.ID(), c.last)
	}
	if err := o.Destroy(); err != nil {
		t.Fatal(err)
	}
	n := len(c.last)
	c.last = c.last[:0]
	if err := o.Destroy(); err == nil {
		t.Fatal("second destructor succeeded")
	}
	if len(c.last) != 0 || n == 0 {
		t.Fatal("wrote after destructor")
	}
	_ = d
}

func TestFDRequest(t *testing.T) {
	sp, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	fa, fb := os.NewFile(uintptr(sp[0]), "a"), os.NewFile(uintptr(sp[1]), "b")
	ca, err := net.FileConn(fa)
	fa.Close()
	if err != nil {
		fb.Close()
		t.Fatal(err)
	}
	defer ca.Close()
	cb, err := net.FileConn(fb)
	fb.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer cb.Close()
	d, err := wlturbo.ConnectFromConn(ca)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	o := NewArgsObj(d.Context())
	o.SetID(d.AllocateID())
	d.Context().Register(o)
	fd, err := unix.Open(os.DevNull, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Give("t", fd, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil {
		t.Fatal("fd not closed after send")
	}
	// The connection first sends wl_display.get_registry (12 bytes).
	buf := make([]byte, 12+8+8+4)
	oob := make([]byte, unix.CmsgSpace(4))
	n, oobn, _, _, err := cb.(*net.UnixConn).ReadMsgUnix(buf, oob)
	if err != nil || n != len(buf) || oobn == 0 {
		t.Fatalf("n=%d oobn=%d err=%v", n, oobn, err)
	}
	// The descriptor has no body word: string(8) + uint(4) only.
	if buf[12+4] != 2 || buf[12+8+8] != 5 || buf[12+6] != 20 {
		t.Fatalf("message = %x", buf[12:])
	}
}
`)
	cmd := exec.Command(goBin, "test", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated request test: %v\n%s", err, out)
	}
}
