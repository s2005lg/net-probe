package retrybackoff

import (
	"testing"
	"time"
)

func TestDelayDoublesAndCaps(t *testing.T) {
	for _, tt := range []struct {
		failures int
		want     time.Duration
	}{
		{failures: 1, want: 5 * time.Minute},
		{failures: 2, want: 10 * time.Minute},
		{failures: 3, want: 20 * time.Minute},
		{failures: 8, want: 6 * time.Hour},
		{failures: 100, want: 6 * time.Hour},
	} {
		if got := Delay(tt.failures); got != tt.want {
			t.Fatalf("Delay(%d) = %s, want %s", tt.failures, got, tt.want)
		}
	}
}
