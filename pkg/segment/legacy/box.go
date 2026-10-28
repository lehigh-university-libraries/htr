// Package legacy preserves Scribe's v1 box-only Kraken and newspaper protocol.
// New consumers use segment/kraken and segment/layoutkraken for real geometry.
package legacy

import (
	"errors"
	"io"
	"os"

	"github.com/lehigh-university-libraries/htr/pkg/remoteocr"
)

type WordBox = remoteocr.Box

func readFileLimit(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	closeErr := file.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if int64(len(data)) > limit {
		return nil, errors.New("legacy segmentation output exceeds limit")
	}
	return data, nil
}
