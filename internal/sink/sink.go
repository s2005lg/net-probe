package sink

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"strings"
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
	nodeID string
	client *http.Client
}

func New(cfg config.Sink, nodeID string) (Sink, error) {
	token, err := config.ResolveToken(cfg)
	if err != nil {
		return nil, err
	}
	method := cfg.Method
	if method == "" {
		method = http.MethodPost
	}
	client := &http.Client{Timeout: 10 * time.Second}
	if cfg.TLSSkipVerify || cfg.TLSCAFile != "" {
		tlsCfg := &tls.Config{InsecureSkipVerify: cfg.TLSSkipVerify} // #nosec G402 — user explicitly enabled
		if cfg.TLSCAFile != "" {
			b, err := os.ReadFile(cfg.TLSCAFile)
			if err != nil {
				return nil, err
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(b) {
				return nil, fmt.Errorf("bad CA file %s", cfg.TLSCAFile)
			}
			tlsCfg.RootCAs = pool
		}
		client.Transport = &http.Transport{TLSClientConfig: tlsCfg}
	}
	return &httpSink{cfg: cfg, token: token, method: method, nodeID: nodeID, client: client}, nil
}

func (s *httpSink) Send(ctx context.Context, body []byte) error {
	url := s.cfg.URL
	if s.cfg.Type == "panel" {
		url = strings.TrimRight(url, "/") + "/api/v1/agents/report"
	}
	req, err := http.NewRequestWithContext(ctx, s.method, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.nodeID != "" {
		req.Header.Set("X-Agent-Id", s.nodeID)
	}
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
