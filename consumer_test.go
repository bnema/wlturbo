//go:build linux

package wlturbo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Run the public socketpair integration against a separate module, with no
// imports from internal/ and no worktree replacement outside its temp go.mod.
func TestExternalConsumer(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go executable unavailable: external module integration requires go")
	}
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	mod := "module example.com/wlturbo-consumer\n\ngo 1.27\n\nrequire github.com/bnema/wlturbo v0.0.0\nreplace github.com/bnema/wlturbo => " + root + "\n"
	if err := os.WriteFile(filepath.Join(temp, "go.mod"), []byte(mod), 0600); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join(root, "protocol/core/core_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	// The external module has no access to the package's test helpers except
	// the ones copied into this temporary module. Compile and exercise only
	// the public integration scenarios, including typed key, frame and release.
	if err := os.WriteFile(filepath.Join(temp, "consumer_test.go"), source, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(goBin, "test", "-run", "TestGeneratedCoreOverSocketpair|TestBufferReleaseAndOfferChild", "./...")
	cmd.Dir = temp
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("external consumer: %v\n%s", err, output)
	}
}
