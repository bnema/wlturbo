package scanner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolution(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fixture.xml")
	xml := `<protocol name="sample"><interface name="wl_surface" version="1"><event name="done"><arg name="value" type="uint"/></event></interface><interface name="custom_thing" version="1"><request name="make"><arg name="id" type="new_id" interface="wl_surface"/><arg name="output" type="object" interface="wl_output"/></request></interface></protocol>`
	if err := os.WriteFile(p, []byte(xml), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewScanner()
	if err := s.ParseXML(p); err != nil {
		t.Fatal(err)
	}
	s.CrossPackage = map[string]string{"wl_surface": "github.com/bnema/wlturbo/protocol/core", "wl_output": "github.com/bnema/wlturbo/protocol/core"}
	b, err := s.Generate("fixture")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, "(*Surface, error)") || !strings.Contains(text, "output *cross_wl_output.Output") || strings.Contains(text, "cross_wl_surface") {
		t.Fatalf("wrong resolution: %s", text)
	}
}
func TestCoreGeneration(t *testing.T) {
	s := NewScanner()
	if err := s.ParseXML("../../protocol/wayland.xml"); err != nil {
		t.Fatal(err)
	}
	b, err := s.Generate("core")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"type Keyboard struct", "type Subsurface struct", "type Buffer struct", "type Callback struct", "return \"uint,fd,uint,\", true", "child := &Keyboard{}"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(string(b), "type Display struct") || strings.Contains(string(b), "type Registry struct") {
		t.Fatal("bootstrap generated twice")
	}
}

// P3 uses wl_buffer as both an event-created child and a request-created child.
// Verify the generated code uses the public constructor instead of embedding
// an inaccessible BaseProxy context field across package boundaries.
func TestExternalChildFactory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "external.xml")
	xml := `<protocol name="fixture"><interface name="ext_params" version="1"><request name="create"><arg name="id" type="new_id" interface="wl_buffer"/></request><event name="created"><arg name="id" type="new_id" interface="wl_buffer"/></event></interface></protocol>`
	if err := os.WriteFile(path, []byte(xml), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewScanner()
	if err := s.ParseXML(path); err != nil {
		t.Fatal(err)
	}
	s.CrossPackage = map[string]string{"wl_buffer": "github.com/bnema/wlturbo/protocol/core"}
	generated, err := s.Generate("ext")
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"cross_wl_buffer.NewBuffer(o.Context())", "(*cross_wl_buffer.Buffer, error)", "idObject.SetID(idID)"} {
		if !strings.Contains(string(generated), part) {
			t.Errorf("generated code missing %q", part)
		}
	}
	if strings.Contains(string(generated), "&cross_wl_buffer.Buffer{}") {
		t.Fatal("foreign child was constructed without its package factory")
	}
}

func TestUnresolvedExternalInterface(t *testing.T) {
	path := filepath.Join(t.TempDir(), "extension.xml")
	xml := `<protocol name="ext"><interface name="ext_parent" version="1"><request name="make"><arg name="id" type="new_id" interface="wl_surface"/></request></interface></protocol>`
	if err := os.WriteFile(path, []byte(xml), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewScanner()
	if err := s.ParseXML(path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Generate("ext"); err == nil || !strings.Contains(err.Error(), "wl_surface") {
		t.Fatalf("unmapped external reference: %v", err)
	}
}

func TestMappedExtensionCompilesAndDispatchesChild(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go unavailable: generated extension compilation requires go")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	xml := `<protocol name="ext"><interface name="ext_parent" version="1"><event name="child"><arg name="id" type="new_id" interface="wl_buffer"/></event></interface></protocol>`
	path := filepath.Join(dir, "ext.xml")
	if err := os.WriteFile(path, []byte(xml), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewScanner()
	if err := s.ParseXML(path); err != nil {
		t.Fatal(err)
	}
	s.CrossPackage = map[string]string{"wl_buffer": "github.com/bnema/wlturbo/protocol/core"}
	generated, err := s.Generate("ext")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ext.go"), generated, 0600); err != nil {
		t.Fatal(err)
	}
	mod := "module example.com/ext\n\ngo 1.27\n\nrequire github.com/bnema/wlturbo v0.0.0\nreplace github.com/bnema/wlturbo => " + root + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0600); err != nil {
		t.Fatal(err)
	}
	test := `package ext
import (
 "encoding/binary"
 "net"
 "os"
 "testing"
 "github.com/bnema/wlturbo"
 "github.com/bnema/wlturbo/protocol/core"
 "golang.org/x/sys/unix"
)
func TestTypedChild(t *testing.T) {
 f, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0); if err != nil { t.Fatal(err) }
 a, b := os.NewFile(uintptr(f[0]), "a"), os.NewFile(uintptr(f[1]), "b")
 c, err := net.FileConn(a); a.Close(); if err != nil { t.Fatal(err) }; defer c.Close()
 p, err := net.FileConn(b); b.Close(); if err != nil { t.Fatal(err) }; defer p.Close()
 d, err := wlturbo.ConnectFromConn(c); if err != nil { t.Fatal(err) }; defer d.Close()
 parent := NewExtParent(d.Context()); parent.SetID(d.AllocateID()); d.Context().Register(parent)
 var child *core.Buffer
 parent.OnChild(func(b *core.Buffer) { child = b })
 frame := make([]byte, 12); binary.LittleEndian.PutUint32(frame, parent.ID()); binary.LittleEndian.PutUint32(frame[4:], 12<<16); binary.LittleEndian.PutUint32(frame[8:], 0xff000001)
 if _, err := p.Write(frame); err != nil { t.Fatal(err) }
 if err := d.Dispatch(); err != nil { t.Fatal(err) }
 if child == nil || child.ID() != 0xff000001 { t.Fatalf("child: %v", child) }
 called := false; child.OnRelease(func(){ called = true })
 frame = frame[:8]; binary.LittleEndian.PutUint32(frame, child.ID()); binary.LittleEndian.PutUint32(frame[4:], 8<<16)
 if _, err := p.Write(frame); err != nil { t.Fatal(err) }
 if err := d.Dispatch(); err != nil { t.Fatal(err) }
 if !called { t.Fatal("typed child did not dispatch release") }
}
`
	if err := os.WriteFile(filepath.Join(dir, "ext_test.go"), []byte(test), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(goBin, "test", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated extension test: %v\n%s", err, out)
	}
}
