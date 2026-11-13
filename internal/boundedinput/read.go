// Package boundedinput applies absolute byte budgets before input decoding.
package boundedinput

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

// Inclusive input byte ceilings, frozen from the reviewed pinned-source sizes.
const (
	GeoSiteBytes    int64 = 32 << 20
	GeoIPBytes      int64 = 64 << 20
	SourceLockBytes int64 = 1 << 20
)

var ErrTooLarge = errors.New("input exceeds byte limit")

// ReadAll probes at most one byte beyond limit. An oversized or failed read
// returns no partial input, so callers cannot accidentally decode truncation.
func ReadAll(reader io.Reader, limit int64) ([]byte, error) {
	if limit < 0 || limit == math.MaxInt64 {
		return nil, fmt.Errorf("input byte limit must be between 0 and %d", int64(math.MaxInt64-1))
	}
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w of %d bytes", ErrTooLarge, limit)
	}
	return data, nil
}

// ReadFile rejects declared oversize early and still limits the read, because
// files may grow after stat and non-regular streams may report no size.
func ReadFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("%w of %d bytes", ErrTooLarge, limit)
	}
	return ReadAll(file, limit)
}
