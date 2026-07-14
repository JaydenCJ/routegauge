// Tests for the input layer: plain files, gzip rotations, stdin, and
// long-line safety.
package logread

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func collect(t *testing.T, name string, stdin *bytes.Buffer) []string {
	t.Helper()
	var got []string
	var in *bytes.Buffer
	if stdin != nil {
		in = stdin
	} else {
		in = &bytes.Buffer{}
	}
	if err := EachLine(name, in, func(line string) { got = append(got, line) }); err != nil {
		t.Fatalf("EachLine(%s) failed: %v", name, err)
	}
	return got
}

func TestPlainFileLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "access.log")
	if err := os.WriteFile(p, []byte("one\ntwo\n\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := collect(t, p, nil)
	if len(got) != 3 || got[0] != "one" || got[2] != "three" {
		t.Fatalf("lines = %v; blanks must be skipped", got)
	}
}

func TestGzipFileTransparentlyDecompressed(t *testing.T) {
	// Rotated logs (access.log.2.gz) must read exactly like plain ones.
	p := filepath.Join(t.TempDir(), "access.log.2.gz")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte("alpha\nbeta\n")); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	got := collect(t, p, nil)
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("gz lines = %v", got)
	}
}

func TestCorruptGzipReportsFilename(t *testing.T) {
	p := filepath.Join(t.TempDir(), "broken.gz")
	if err := os.WriteFile(p, []byte("this is not gzip"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := EachLine(p, &bytes.Buffer{}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "broken.gz") {
		t.Fatalf("err = %v, want a filename-qualified gzip error", err)
	}
}

func TestStdinDash(t *testing.T) {
	got := collect(t, "-", bytes.NewBufferString("from stdin\n"))
	if len(got) != 1 || got[0] != "from stdin" {
		t.Fatalf("stdin lines = %v", got)
	}
}

func TestMissingFileErrors(t *testing.T) {
	if err := EachLine(filepath.Join(t.TempDir(), "nope.log"), &bytes.Buffer{}, func(string) {}); err == nil {
		t.Fatal("missing file must error")
	}
}

func TestLongLinesSurvive(t *testing.T) {
	// A 200 KiB user agent must not abort the scan (default bufio
	// scanner caps at 64 KiB).
	p := filepath.Join(t.TempDir(), "long.log")
	long := "start " + strings.Repeat("x", 200*1024) + " end"
	if err := os.WriteFile(p, []byte(long+"\nnext\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := collect(t, p, nil)
	if len(got) != 2 || !strings.HasSuffix(got[0], " end") {
		t.Fatalf("long line handling broken: %d lines", len(got))
	}
}

func TestSurroundingWhitespaceTrimmed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "pad.log")
	if err := os.WriteFile(p, []byte("  padded line \t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := collect(t, p, nil)
	if len(got) != 1 || got[0] != "padded line" {
		t.Fatalf("trim broken: %q", got)
	}
}
