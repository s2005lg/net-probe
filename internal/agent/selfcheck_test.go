package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSelfCheckRejectsUnknownAndDuplicateIdentifiers(t *testing.T) {
	checker := &SelfChecker{Checks: map[CheckID]CheckFunc{CheckConfig: func(context.Context) error { return nil }}}
	for _, payload := range []string{
		`{"checks":["unknown"]}`,
		`{"checks":["config","config"]}`,
		`{"checks":["config"],"extra":true}`,
	} {
		outcome := checker.Run(context.Background(), json.RawMessage(payload))
		if !outcome.Failed || outcome.Code != "invalid_payload" || string(outcome.Data) != `{}` {
			t.Fatalf("payload=%s outcome=%+v", payload, outcome)
		}
	}
}

func TestSelfCheckReturnsOnlyBoundedFixedResults(t *testing.T) {
	const secret = "fixture-secret-from-raw-error"
	checker := &SelfChecker{
		Timeout: 20 * time.Millisecond,
		Checks: map[CheckID]CheckFunc{
			CheckConfig: func(context.Context) error { return nil },
			CheckPanel:  func(context.Context) error { return errors.New(secret) },
			CheckUpdate: func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			},
		},
	}
	outcome := checker.Run(context.Background(), json.RawMessage(`{"checks":["config","panel","update"]}`))
	if outcome.Failed || outcome.Code != "self_check_completed" || len(outcome.Data) > 16*1024 {
		t.Fatalf("outcome=%+v", outcome)
	}
	if strings.Contains(string(outcome.Data), secret) || strings.Contains(string(outcome.Data), "context deadline") {
		t.Fatalf("result leaked internal error: %s", outcome.Data)
	}
	var result struct {
		Checks []CheckResult `json:"checks"`
	}
	if err := json.Unmarshal(outcome.Data, &result); err != nil {
		t.Fatal(err)
	}
	want := []CheckResult{
		{Check: CheckConfig, Status: "ok", Code: "ok"},
		{Check: CheckPanel, Status: "error", Code: "check_failed"},
		{Check: CheckUpdate, Status: "error", Code: "timeout"},
	}
	if len(result.Checks) != len(want) {
		t.Fatalf("checks=%+v", result.Checks)
	}
	for index := range want {
		if result.Checks[index] != want[index] {
			t.Fatalf("check[%d]=%+v want=%+v", index, result.Checks[index], want[index])
		}
	}
}
