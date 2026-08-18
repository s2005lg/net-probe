package detect

import (
	"context"
	"strings"
	"time"
)

func Version(ctx context.Context, r Runner, binary string, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := r.Run(ctx, binary, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
