package scanner

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Check every checked-in binding against its generation command, including
// external type mappings. Regeneration must not depend on the host's XML files.
func TestVendoredProtocolsReproduce(t *testing.T) {
	root := filepath.Join("..", "..", "protocol")
	referenced := make(map[string]bool)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Name() != "generate.go" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(content), "\n") {
			if !strings.HasPrefix(line, "//go:generate go run ") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 5 || !strings.HasSuffix(fields[3], "/cmd/wlturbo-scanner") {
				t.Fatalf("%s: expected direct go run of cmd/wlturbo-scanner", path)
			}
			args := fields[4:]
			var pkg, output, input string
			imports := make(map[string]string)
			for i := 0; i < len(args); i++ {
				switch args[i] {
				case "-p", "-o", "-import":
					if i+1 == len(args) {
						t.Fatalf("%s: missing value for %s", path, args[i])
					}
					flag, value := args[i], args[i+1]
					i++
					switch flag {
					case "-p":
						pkg = value
					case "-o":
						output = value
					case "-import":
						iface, target, ok := strings.Cut(value, "=")
						if !ok {
							t.Fatalf("%s: invalid import %q", path, value)
						}
						imports[iface] = target
					}
				default:
					if strings.HasPrefix(args[i], "-") || input != "" {
						t.Fatalf("%s: unexpected generation argument %q", path, args[i])
					}
					input = args[i]
				}
			}
			if pkg == "" || output == "" || input == "" {
				t.Fatalf("%s: incomplete generation command", path)
			}
			dir := filepath.Dir(path)
			xmlPath := filepath.Clean(filepath.Join(dir, input))
			if referenced[xmlPath] {
				t.Fatalf("%s generated more than once", xmlPath)
			}
			referenced[xmlPath] = true
			t.Run(pkg, func(t *testing.T) {
				s := NewScanner()
				s.CrossPackage = imports
				if err := s.ParseXML(xmlPath); err != nil {
					t.Fatal(err)
				}
				generated, err := s.Generate(pkg)
				if err != nil {
					t.Fatal(err)
				}
				checkedIn, err := os.ReadFile(filepath.Join(dir, output))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(generated, checkedIn) {
					t.Fatal("bindings are stale; run GOWORK=off go generate ./...")
				}
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(referenced) == 0 {
		t.Fatal("no vendored protocols found")
	}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && filepath.Ext(path) == ".xml" && !referenced[path] {
			t.Errorf("%s has no generation command", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
