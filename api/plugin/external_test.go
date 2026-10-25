package plugin_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// A distinct module enforces Go's internal-package import boundary. Both
// compiled-in examples must remain usable by external plugin authors.
func TestExternalModuleCanImportPluginAPIAndExamples(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	temp := t.TempDir()
	mod := "module external-plugin-test\n\ngo 1.27.1\n\nrequire mc-go v0.0.0\nreplace mc-go => " + filepath.ToSlash(root) + "\n"
	if err := os.WriteFile(filepath.Join(temp, "go.mod"), []byte(mod), 0600); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join(root, "api/plugin/testdata/external/plugin_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(temp, "plugin_test.go"), fixture, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "test", "-mod=mod", "./...")
	cmd.Dir = temp
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("external module: %v\n%s", err, output)
	}
}
