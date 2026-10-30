// Command collisionfull records full collision-cube facts from the pinned
// native server through public getters. It does not start a world/server.
package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"flag"
	"fmt"
	"io/fs"
	"math/bits"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed FullCubeProbe.java
var probe string

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	jar := flag.String("jar", "vanilla/versions/26.2/server-26.2.jar", "unpacked native server jar")
	libraries := flag.String("libraries", "vanilla/libraries", "bundled library directory")
	output := flag.String("out", "", "output fixture (default stdout)")
	flag.Parse()
	serverJar, err := filepath.Abs(*jar)
	if err != nil {
		return err
	}
	rawJar, err := os.ReadFile(serverJar)
	if err != nil {
		return err
	}
	var classpath []string
	err = filepath.WalkDir(*libraries, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".jar") {
			absolute, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			classpath = append(classpath, absolute)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(classpath)
	classpath = append([]string{serverJar}, classpath...)
	directory, err := os.MkdirTemp("", "mcgo-collisionfull-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	source := filepath.Join(directory, "FullCubeProbe.java")
	if err := os.WriteFile(source, []byte(probe), 0644); err != nil {
		return err
	}
	command := exec.Command("java", "-Xmx1G", "-XX:ActiveProcessorCount=2", "--class-path", strings.Join(classpath, string(os.PathListSeparator)), source)
	command.Dir = directory
	measured, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("native getter probe: %w\n%s", err, measured)
	}
	var encoded string
	rows, maxID := 0, 0
	for _, line := range strings.Split(string(measured), "\n") {
		if i := strings.Index(line, "FULL_STATES "); i >= 0 {
			if _, err := fmt.Sscanf(line[i:], "FULL_STATES %d MAX_ID %d", &rows, &maxID); err != nil {
				return err
			}
		}
		if i := strings.Index(line, "FULL_BITS "); i >= 0 {
			encoded = strings.TrimSpace(line[i+len("FULL_BITS "):])
		}
	}
	data, err := hex.DecodeString(encoded)
	if err != nil {
		return err
	}
	if rows == 0 || maxID+1 != rows || len(data) != (rows+7)/8 {
		return fmt.Errorf("incomplete getter coverage: rows%d max%d bytes%d", rows, maxID, len(data))
	}
	full := 0
	for _, b := range data {
		full += bits.OnesCount8(b)
	}
	var result strings.Builder
	fmt.Fprintf(&result, "# Native 26.2 BlockState.isCollisionShapeFullBlock(EmptyBlockGetter.INSTANCE, BlockPos.ZERO)\n# Jar SHA256 %x\n# Probe SHA256 %x\n# Rows %d; full %d; little-endian bit order within each byte.\n", sha256.Sum256(rawJar), sha256.Sum256([]byte(probe)), rows, full)
	for start := 0; start < len(encoded); start += 128 {
		end := min(start+128, len(encoded))
		result.WriteString(encoded[start:end])
		result.WriteByte('\n')
	}
	if *output == "" {
		_, err = os.Stdout.WriteString(result.String())
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(*output), ".collisionfull-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	defer temporary.Close()
	if err := temporary.Chmod(0644); err != nil {
		return err
	}
	if _, err := temporary.WriteString(result.String()); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), *output)
}
