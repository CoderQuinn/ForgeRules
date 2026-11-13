package boundedinput

import (
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Removing the limit-plus-one probe would accept the oversized case; limiting
// to limit rather than limit+1 would silently truncate it into a success.
func TestReadAllBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, input, want string
		limit             int64
		oversized         bool
	}{
		{name: "empty", limit: 4},
		{name: "zero empty", limit: 0},
		{name: "under", input: "abc", want: "abc", limit: 4},
		{name: "exact", input: "abcd", want: "abcd", limit: 4},
		{name: "one over", input: "abcde", limit: 4, oversized: true},
		{name: "zero over", input: "a", limit: 0, oversized: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, err := ReadAll(strings.NewReader(test.input), test.limit)
			if test.oversized {
				if !errors.Is(err, ErrTooLarge) || data != nil {
					t.Fatalf("data=%q error=%v", data, err)
				}
				return
			}
			if err != nil || string(data) != test.want {
				t.Fatalf("data=%q error=%v want=%q", data, err, test.want)
			}
		})
	}
}

func TestReadAllStopsUnknownLengthStreamAtProbe(t *testing.T) {
	reader := &generatedReader{}
	data, err := ReadAll(reader, 16)
	if !errors.Is(err, ErrTooLarge) || data != nil {
		t.Fatalf("data=%q error=%v", data, err)
	}
	if reader.read != 17 {
		t.Fatalf("consumed %d bytes, want 17", reader.read)
	}
}

func TestReadAllPropagatesFailureWithoutPartialData(t *testing.T) {
	failure := errors.New("injected read failure")
	for _, limit := range []int64{3, 4} {
		data, err := ReadAll(&failingReader{failure: failure}, limit)
		if !errors.Is(err, failure) || data != nil {
			t.Fatalf("limit %d: data=%q error=%v", limit, data, err)
		}
	}
}

func TestReadAllRejectsUnsafeLimitsBeforeReading(t *testing.T) {
	for _, limit := range []int64{-1, math.MaxInt64} {
		reader := &generatedReader{}
		if _, err := ReadAll(reader, limit); err == nil || reader.read != 0 {
			t.Fatalf("limit=%d error=%v consumed=%d", limit, err, reader.read)
		}
	}
}

// Models a file that was within budget when stat ran, then grows during read.
// A stat-only implementation would accept the extra bytes.
func TestReadAllRejectsFileGrowingAfterStat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "growing")
	if err := os.WriteFile(path, []byte("ab"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() != 2 {
		t.Fatalf("initial stat = %v, %v", info, err)
	}
	reader := &growingReader{file: file, path: path}
	data, err := ReadAll(reader, 4)
	if !errors.Is(err, ErrTooLarge) || data != nil {
		t.Fatalf("growing input data=%q error=%v", data, err)
	}
}

func TestReadFileBoundariesSparseAndReadFailure(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "input")
	for _, contents := range []string{"", "abcd", "abcde"} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		data, err := ReadFile(path, 4)
		if len(contents) > 4 {
			if !errors.Is(err, ErrTooLarge) || data != nil {
				t.Fatalf("oversized data=%q error=%v", data, err)
			}
		} else if err != nil || string(data) != contents {
			t.Fatalf("data=%q error=%v", data, err)
		}
	}
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(1 << 40); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if data, err := ReadFile(path, 4); !errors.Is(err, ErrTooLarge) || data != nil {
		t.Fatalf("sparse data=%q error=%v", data, err)
	}
	if _, err := ReadFile(filepath.Join(root, "missing"), 4); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing input error=%v", err)
	}
	if _, err := ReadFile(root, math.MaxInt64-1); err == nil || errors.Is(err, ErrTooLarge) {
		t.Fatal("directory read succeeded")
	}
}

type generatedReader struct{ read int }

func (r *generatedReader) Read(p []byte) (int, error) {
	// Finite backing protects the test from hanging if the production guard is
	// removed; the reader still exposes no size to the code under test.
	if r.read == 1024 {
		return 0, io.EOF
	}
	n := min(len(p), 1024-r.read)
	for i := range p[:n] {
		p[i] = 'x'
	}
	r.read += n
	return n, nil
}

type failingReader struct{ failure error }

func (r *failingReader) Read(p []byte) (int, error) { return copy(p, "abc"), r.failure }

type growingReader struct {
	file *os.File
	path string
	grew bool
}

func (r *growingReader) Read(p []byte) (int, error) {
	if !r.grew {
		r.grew = true
		file, err := os.OpenFile(r.path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return 0, err
		}
		_, err = file.WriteString("cde")
		closeErr := file.Close()
		if err != nil {
			return 0, err
		}
		if closeErr != nil {
			return 0, closeErr
		}
	}
	return r.file.Read(p)
}
