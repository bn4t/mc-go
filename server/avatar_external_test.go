package server_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// A separate module enforces Go's internal import boundary for the complete
// admission/replication consumer, not merely compilation of API declarations.
func TestExternalAvatarFrontend(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(file))
	temp := t.TempDir()
	mod := "module external-avatar-test\n\ngo 1.27.1\n\nrequire mc-go v0.0.0\nreplace mc-go => " + filepath.ToSlash(root) + "\n"
	if err := os.WriteFile(filepath.Join(temp, "go.mod"), []byte(mod), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"avatar_test.go", "wire_test.go"} {
		b, err := os.ReadFile(filepath.Join(root, "server/testdata/external-avatar", name))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(temp, name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"test", "-mod=mod", "-count=1", "-v", "-timeout=180s"}
	if externalAvatarRace {
		args = append(args, "-race")
	}
	args = append(args, "./...")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin/go"), args...)
	cmd.Dir = temp
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("external avatar consumer: %v\n%s", err, output)
	}
	t.Logf("external avatar consumer:\n%s", output)
}
