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
	return parseVersion(out), nil
}

func parseVersion(out string) string {
	out = strings.TrimSpace(out)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Version:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Version:"))
		}
	}
	return out
}
