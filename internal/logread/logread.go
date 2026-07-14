// Package logread streams lines out of access-log files: plain files,
// gzip-compressed rotations (access.log.2.gz reads exactly like
// access.log), and stdin via the conventional "-".
package logread

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"strings"
)

// maxLineBytes bounds a single log line; combined lines with huge user
// agents stay far below this.
const maxLineBytes = 1 << 20

// EachLine opens the named source and invokes fn for every non-blank
// line. name "-" reads stdin (from the given reader, so tests can
// substitute one); a ".gz" suffix enables transparent decompression.
func EachLine(name string, stdin io.Reader, fn func(line string)) error {
	var r io.Reader
	if name == "-" {
		r = stdin
	} else {
		f, err := os.Open(name)
		if err != nil {
			return err
		}
		defer f.Close()
		r = f
		if strings.HasSuffix(name, ".gz") {
			gz, err := gzip.NewReader(f)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			defer gz.Close()
			r = gz
		}
	}
	return scan(name, r, fn)
}

func scan(name string, r io.Reader, fn func(line string)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), maxLineBytes)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		fn(line)
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}
