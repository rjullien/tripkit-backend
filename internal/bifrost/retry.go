package bifrost

import "time"

// WithRetry retries fn up to attempts times with linear backoff (1s, 2s, 3s...).
// Returns on the first success or the last error.
func WithRetry(attempts int, fn func() (string, error)) (string, error) {
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		result, err := fn()
		if err == nil {
			return result, nil
		}
		lastErr = err
		if i < attempts-1 {
			time.Sleep(time.Duration(i+1) * time.Second)
		}
	}
	return "", lastErr
}
