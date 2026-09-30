package scanner

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestIncludedLGPLLicense(t *testing.T) {
	content, err := os.ReadFile("../../LICENSES/LGPL-2.1-or-later.txt")
	if err != nil {
		t.Fatal(err)
	}
	const expected = "5749785c8bdefafcb5d798270ed0a967036fe2ca63dcedade1627565dfef81d2"
	if got := fmt.Sprintf("%x", sha256.Sum256(content)); got != expected {
		t.Fatalf("LGPL text checksum %s, want %s", got, expected)
	}
}

func TestGeneratedSourceRetainsProtocolLicense(t *testing.T) {
	for _, source := range []string{
		"../../protocol/kdeserverdecoration/server-decoration.xml",
		"../../protocol/layershell/wlr-layer-shell-unstable-v1.xml",
		"../../protocol/wayland.xml",
	} {
		t.Run(source, func(t *testing.T) {
			s := NewScanner()
			if err := s.ParseXML(source); err != nil {
				t.Fatal(err)
			}
			s.CrossPackage = map[string]string{
				"wl_surface": "github.com/bnema/wlturbo/protocol/core",
				"wl_output":  "github.com/bnema/wlturbo/protocol/core",
				"xdg_popup":  "github.com/bnema/wlturbo/protocol/xdgshell",
			}
			generated, err := s.Generate("core")
			if err != nil {
				t.Fatal(err)
			}
			header, _, ok := strings.Cut(string(generated), "\npackage core")
			if !ok {
				t.Fatal("package declaration missing")
			}
			for _, line := range strings.Split(strings.TrimSpace(s.protocol.Copyright), "\n") {
				if text := strings.TrimSpace(line); text != "" && !strings.Contains(header, text) {
					t.Errorf("upstream license line missing from header: %q", text)
				}
			}
		})
	}
}
