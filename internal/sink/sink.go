package sink

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/s2005lg/net-probe/internal/config"
)

type Sink interface {
	Send(ctx context.Context, body []byte) error
}

type httpSink struct {
	cfg    config.Sink
	token  string
	method string
	client *http.Client
}

func New(cfg config.Sink) (Sink, error) {
	token, err := config.ResolveToken(cfg)
	if err != nil {
		return nil, err
	}
	method := cfg.Method
	if method == "" {
		method = http.MethodPost
	}
	return &httpSink{cfg: cfg, token: token, method: method, client: &http.Client{Timeout: 10 * time.Second}}, nil
}

func (s *httpSink) Send(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, s.method, s.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	for k, v := range s.cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("sink %s returned %d", s.cfg.URL, resp.StatusCode)
	}
	return nil
}
