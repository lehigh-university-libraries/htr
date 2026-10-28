package kraken

import (
	"context"
	"fmt"
	"strings"

	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

// stderrTail bounds diagnostics while retaining the end of long tracebacks.
type stderrTail struct{ data []byte }

func (w *stderrTail) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) >= 4096 {
		w.data = append(w.data[:0], p[len(p)-4096:]...)
	} else {
		if len(w.data)+len(p) > 4096 {
			w.data = w.data[len(w.data)+len(p)-4096:]
		}
		w.data = append(w.data, p...)
	}
	return n, nil
}
func (w *stderrTail) failure(ctx context.Context, err error, paths ...string) error {
	text := strings.TrimSpace(string(w.data))
	for _, path := range paths {
		if path != "" {
			text = strings.ReplaceAll(text, path, "[path]")
		}
	}
	if text == "" {
		return providers.ErrorForRequest(ctx, err)
	}
	return fmt.Errorf("%w: kraken: %s", providers.ErrorForRequest(ctx, err), text)
}
