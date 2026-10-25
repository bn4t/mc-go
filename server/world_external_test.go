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

// The complete finite-volume consumer runs outside mc-go's internal import
// boundary. Existing avatar helpers supply only public capabilities and Java TCP.
func TestExternalWorldFrontend(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(file))
	temp := t.TempDir()
	mod := "module external-world-test\n\ngo 1.27.1\n\nrequire mc-go v0.0.0\nreplace mc-go => " + filepath.ToSlash(root) + "\n"
	if e := os.WriteFile(filepath.Join(temp, "go.mod"), []byte(mod), 0600); e != nil {
		t.Fatal(e)
	}
	for _, source := range []string{"external-avatar/avatar_test.go", "external-avatar/wire_test.go", "external-world/world_test.go", "external-world/block_wire_test.go", "external-world/facts.json"} {
		b, e := os.ReadFile(filepath.Join(root, "server/testdata", source))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(temp, filepath.Base(source)), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	args := []string{"test", "-mod=mod", "-count=1", "-v", "-run", "^TestPublicWorld", "-timeout=180s"}
	if externalAvatarRace {
		args = append(args, "-race")
	}
	args = append(args, "./...")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin/go"), args...)
	cmd.Dir = temp
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off")
	b, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("external world consumer: %v\n%s", e, b)
	}
	t.Logf("external world consumer:\n%s", b)
}
