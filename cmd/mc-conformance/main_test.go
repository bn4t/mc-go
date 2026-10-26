package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mc-go/conformance"
)

func TestCLIReportExitCodes(t *testing.T) {
	var out, errout bytes.Buffer
	if code := run([]string{"-endpoint", "invalid"}, &out, &errout); code != 2 {
		t.Fatalf("code %d: %s", code, errout.String())
	}
	var report conformance.Report
	if e := json.Unmarshal(out.Bytes(), &report); e != nil {
		t.Fatal(e)
	}
	if report.Results[0].Status != conformance.Infrastructure {
		t.Fatal(report)
	}
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Skip(e)
	}
	defer ln.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, e := ln.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		var h [1]byte
		if _, e = io.ReadFull(c, h[:]); e != nil {
			return
		}
		b := make([]byte, int(h[0])+2)
		if _, e = io.ReadFull(c, b); e != nil {
			return
		}
		_, _ = c.Write([]byte{4, 0, 2, '{', '}'})
	}()
	out.Reset()
	errout.Reset()
	if code := run([]string{"-endpoint", ln.Addr().String(), "-capabilities", "status"}, &out, &errout); code != 1 {
		t.Fatalf("bad schema returned %d: %s", code, errout.String())
	}
	<-done
	if e = json.Unmarshal(out.Bytes(), &report); e != nil {
		t.Fatal(e)
	}
	if report.Results[0].Status != conformance.Fail {
		t.Fatal(report)
	}
}
func TestCLIRejectsUnknownManifestFields(t *testing.T) {
	p := filepath.Join(t.TempDir(), "target.json")
	if e := os.WriteFile(p, []byte(`{"endpoint":"127.0.0.1:1","typo":true}`), 0600); e != nil {
		t.Fatal(e)
	}
	var out, errout bytes.Buffer
	if code := run([]string{"-config", p}, &out, &errout); code != 2 {
		t.Fatalf("code %d", code)
	}
	if out.Len() != 0 {
		t.Fatal("invalid manifest emitted a success report")
	}
}

func TestCLIUnsupportedProfileIsNonzero(t *testing.T) {
	var out, errout bytes.Buffer
	code := run([]string{"-endpoint", "example.invalid:25565", "-protocol", "774", "-capabilities", "configuration"}, &out, &errout)
	var report conformance.Report
	if e := json.Unmarshal(out.Bytes(), &report); e != nil {
		t.Fatal(e)
	}
	if code != 2 || report.Reason == "" {
		t.Fatalf("unsupported profile returned %d: %s", code, out.String())
	}
}

func TestCLIPlayChunkManifestContract(t *testing.T) {
	for _, tc := range []struct {
		name, manifest, detail string
		parseError             bool
	}{
		{"unknown-shape-field", `{"endpoint":"example.invalid:25565","capabilities":["play-chunks"],"play":{"shape":{"sections":2,"block_states":8,"biomes":1,"typo":1}}}`, "unknown field", true},
		{"invalid-shape", `{"endpoint":"example.invalid:25565","capabilities":["play-chunks"],"play":{"shape":{"sections":0,"block_states":8,"biomes":1}}}`, "play.shape", false},
		{"missing-shape", `{"endpoint":"example.invalid:25565","capabilities":["play-chunks"]}`, "explicit play.shape", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "endpoint.json")
			if err := os.WriteFile(path, []byte(tc.manifest), 0600); err != nil {
				t.Fatal(err)
			}
			var out, errout bytes.Buffer
			if code := run([]string{"-config", path}, &out, &errout); code != 2 {
				t.Fatalf("invalid manifest returned %d: %s", code, out.String())
			}
			if !strings.Contains(errout.String(), tc.detail) || strings.Contains(errout.String(), "dial:") {
				t.Fatalf("unexpected manifest error: %s", errout.String())
			}
			if tc.parseError {
				if out.Len() != 0 {
					t.Fatal("invalid JSON schema emitted a protocol report")
				}
				return
			}
			var report conformance.Report
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if len(report.Results) != 1 || report.Results[0].Status != conformance.Infrastructure {
				t.Fatalf("invalid fixture reached protocol execution: %+v", report)
			}
		})
	}

	path := filepath.Join(t.TempDir(), "unsupported.json")
	manifest := `{"endpoint":"example.invalid:25565","protocol":774,"capabilities":["play-chunks"],"play":{"shape":{"sections":2,"block_states":8,"biomes":1}}}`
	if err := os.WriteFile(path, []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errout bytes.Buffer
	if code := run([]string{"-config", path}, &out, &errout); code != 2 {
		t.Fatalf("unsupported profile returned %d: %s", code, out.String())
	}
	var report conformance.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Target.Play == nil || report.Target.Play.Shape != (conformance.ChunkShape{Sections: 2, BlockStates: 8, Biomes: 1}) {
		t.Fatalf("decoded fixture contract was lost from report: %+v", report.Target)
	}
	for _, result := range report.Results {
		if result.Scenario == "play.chunk-window" {
			if result.Status != conformance.Unsupported || !result.Declared || strings.Contains(result.Detail, "dial:") {
				t.Fatalf("unsupported fixture contract reached network: %+v", result)
			}
			return
		}
	}
	t.Fatal("missing play.chunk-window result")
}
