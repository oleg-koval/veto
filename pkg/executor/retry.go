package executor

import (
	"net/http"
	"strconv"
	"time"
)

const maxRetries = 3

// retryableStatus returns true for transient server errors worth retrying.
func retryableStatus(code int) bool {
	return code == 429 || code == 502 || code == 503 || code == 504
}

// retryDelay returns the base wait time before the nth retry (1-indexed).
func retryDelay(attempt int) time.Duration {
	if attempt <= 1 {
		return time.Second
	}
	return 2 * time.Second
}

// retryAfter reads the Retry-After header (integer seconds only) and caps at 30s.
// Falls back to defaultDelay when the header is absent or unparseable.
func retryAfter(resp *http.Response, defaultDelay time.Duration) time.Duration {
	if s := resp.Header.Get("Retry-After"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 30 {
			return time.Duration(n) * time.Second
		}
	}
	return defaultDelay
}
