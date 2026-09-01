package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/s2005lg/net-probe/internal/controlproto"
	"github.com/s2005lg/net-probe/internal/detect"
)

type CheckID string

const (
	CheckConfig      CheckID = "config"
	CheckCertificate CheckID = "certificate"
	CheckPanel       CheckID = "panel"
	CheckDetectors   CheckID = "detectors"
	CheckFilesystem  CheckID = "filesystem"
	CheckUpdate      CheckID = "update"
)

type SelfCheckPayload struct {
	Checks []CheckID `json:"checks"`
}

type CheckResult struct {
	Check  CheckID `json:"check"`
	Status string  `json:"status"`
	Code   string  `json:"code"`
}

type CheckFunc func(context.Context) error

type SelfChecker struct {
	Checks  map[CheckID]CheckFunc
	Timeout time.Duration
}

func (s *SelfChecker) Run(ctx context.Context, raw json.RawMessage) CommandOutcome {
	invalid := CommandOutcome{Code: "invalid_payload", Data: json.RawMessage(`{}`), Failed: true}
	if s == nil || len(s.Checks) == 0 {
		return CommandOutcome{Code: "self_check_unavailable", Data: json.RawMessage(`{}`), Failed: true}
	}
	var payload SelfCheckPayload
	if err := controlproto.StrictDecodePayload(raw, &payload); err != nil || len(payload.Checks) == 0 || len(payload.Checks) > 6 {
		return invalid
	}
	seen := make(map[CheckID]struct{}, len(payload.Checks))
	for _, check := range payload.Checks {
		if s.Checks[check] == nil {
			return invalid
		}
		if _, exists := seen[check]; exists {
			return invalid
		}
		seen[check] = struct{}{}
	}
	timeout := s.Timeout
	if timeout <= 0 || timeout > 5*time.Second {
		timeout = 3 * time.Second
	}
	results := make([]CheckResult, 0, len(payload.Checks))
	for _, check := range payload.Checks {
		checkContext, cancel := context.WithTimeout(ctx, timeout)
		err := s.Checks[check](checkContext)
		deadline := errors.Is(err, context.DeadlineExceeded) || errors.Is(checkContext.Err(), context.DeadlineExceeded)
		cancel()
		result := CheckResult{Check: check, Status: "ok", Code: "ok"}
		if deadline {
			result.Status, result.Code = "error", "timeout"
		} else if err != nil {
			result.Status, result.Code = "error", "check_failed"
		}
		results = append(results, result)
	}
	body, err := json.Marshal(struct {
		Checks []CheckResult `json:"checks"`
	}{Checks: results})
	if err != nil || len(body) > 16*1024 {
		return CommandOutcome{Code: "self_check_failed", Data: json.RawMessage(`{}`), Failed: true}
	}
	return CommandOutcome{Code: "self_check_completed", Data: body}
}

func (r *Runtime) selfChecker() *SelfChecker {
	snapshot := r.snapshot()
	return &SelfChecker{Checks: map[CheckID]CheckFunc{
		CheckConfig: func(context.Context) error {
			if snapshot == nil || snapshot.cfg == nil {
				return errors.New("configuration unavailable")
			}
			return snapshot.cfg.Validate()
		},
		CheckCertificate: func(context.Context) error {
			if snapshot == nil || snapshot.identity == nil || snapshot.identity.leaf == nil || time.Until(snapshot.identity.leaf.NotAfter) <= 0 {
				return errors.New("certificate unavailable")
			}
			return nil
		},
		CheckPanel: func(ctx context.Context) error {
			if snapshot == nil || snapshot.cfg == nil || snapshot.identity == nil || snapshot.identity.TLSConfig == nil {
				return errors.New("Panel unavailable")
			}
			panelURL, err := url.Parse(snapshot.cfg.Panel.URL)
			if err != nil {
				return err
			}
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpointURL(panelURL, "/api/v1/ca"), nil)
			if err != nil {
				return err
			}
			client := &http.Client{Transport: &http.Transport{TLSClientConfig: snapshot.identity.TLSConfig.Clone()}}
			defer client.CloseIdleConnections()
			response, err := client.Do(request)
			if err != nil {
				return err
			}
			defer response.Body.Close()
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
			if response.StatusCode != http.StatusOK {
				return errors.New("Panel check rejected")
			}
			return nil
		},
		CheckDetectors: func(context.Context) error {
			if snapshot == nil || snapshot.cfg == nil {
				return errors.New("detector configuration unavailable")
			}
			templates, err := detect.Builtin()
			if err != nil {
				return err
			}
			custom, err := detect.LoadCustom(snapshot.cfg.Detect.CustomDir)
			if err != nil {
				return err
			}
			_, err = detect.NewRegistry(append(templates, custom...))
			return err
		},
		CheckFilesystem: func(context.Context) error {
			dir := StateDir()
			file, err := os.CreateTemp(dir, ".self-check-*")
			if err != nil {
				return err
			}
			name := file.Name()
			defer os.Remove(name)
			if err := file.Chmod(0o600); err != nil {
				file.Close()
				return err
			}
			if _, err := file.Write([]byte("ok")); err != nil {
				file.Close()
				return err
			}
			if err := file.Sync(); err != nil {
				file.Close()
				return err
			}
			return file.Close()
		},
		CheckUpdate: func(context.Context) error {
			if snapshot == nil || snapshot.cfg == nil || snapshot.identity == nil || len(snapshot.identity.ReleaseKey) != ed25519.PublicKeySize {
				return errors.New("update verification unavailable")
			}
			return nil
		},
	}}
}

func exactEmptyObject(raw json.RawMessage) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload map[string]json.RawMessage
	if err := decoder.Decode(&payload); err != nil || payload == nil || len(payload) != 0 {
		return false
	}
	return decoder.Decode(&struct{}{}) == io.EOF
}
