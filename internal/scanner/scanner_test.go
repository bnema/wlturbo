package scanner

import (
	"os"
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
