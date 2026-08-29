package retrybackoff

import "time"

const (
	Initial = 5 * time.Minute
	Maximum = 6 * time.Hour
)

// Delay returns a bounded exponential delay for a one-based failure count.
func Delay(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	delay := Initial
	for i := 1; i < failures && delay < Maximum; i++ {
		if delay >= Maximum/2 {
			return Maximum
		}
		delay *= 2
	}
	if delay > Maximum {
		return Maximum
	}
	return delay
}
