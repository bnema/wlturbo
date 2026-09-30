package scanner

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestProtocolSourceChecksums(t *testing.T) {
	root := filepath.Join("..", "..", "protocol")
	content, err := os.ReadFile(filepath.Join(root, "SOURCE.md"))
	if err != nil {
		t.Fatal(err)
	}
	coreChecksum := regexp.MustCompile("(?m)^SHA-256: `([a-f0-9]{64})`\\.").FindSubmatch(content)
	if coreChecksum == nil {
		t.Fatal("core protocol source checksum is missing")
	}
	// Extension tables record upstream path, vendored path and SHA-256.
	rows := regexp.MustCompile("(?m)^\\| `[^`]+` \\| `([^`]+)` \\| `([a-f0-9]{64})` \\|$").FindAllSubmatch(content, -1)
	if len(rows) == 0 {
		t.Fatal("no protocol source checksums found")
	}
	rows = append(rows, [][]byte{nil, []byte("wayland.xml"), coreChecksum[1]})
	recorded := make(map[string]bool)
	for _, row := range rows {
		path, want := string(row[1]), string(row[2])
		if !filepath.IsLocal(path) {
			t.Fatalf("source path must be repository-relative: %q", path)
		}
		if recorded[path] {
			t.Fatalf("duplicate source record: %s", path)
		}
		recorded[path] = true
		t.Run(path, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, path))
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != want {
				t.Fatalf("SHA-256 %s, recorded %s", got, want)
			}
			if !strings.Contains(string(data), "<copyright>") {
				t.Fatal("vendored XML is missing upstream copyright/license")
			}
		})
	}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".xml" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if !recorded[filepath.ToSlash(rel)] {
			t.Errorf("%s has no source/checksum record", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
