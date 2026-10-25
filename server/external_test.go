package server_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// The separate consumer also runs from git-archive validation. Testing only the
// working tree can miss public packages accidentally excluded by .gitignore.
func TestExternalModuleImportsPublicServerAndRegistry(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	module := fmt.Sprintf("module example.com/public-consumer\n\ngo 1.27.1\n\nrequire mc-go v0.0.0\nreplace mc-go => %q\n", filepath.ToSlash(root))
	if err := os.WriteFile(filepath.Join(temp, "go.mod"), []byte(module), 0600); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join("testdata", "external", "import_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(temp, "import_test.go"), fixture, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "test", "-mod=mod", "./...")
	cmd.Dir = temp
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("external public consumer: %v\n%s", err, output)
	}
}
