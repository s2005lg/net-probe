package controlproto

import (
	"strings"
	"testing"
)

func TestStrictDecodeCommand(t *testing.T) {
	good := `{"control_version":"1","type":"command","command_id":"c1","sequence":1,"agent_id":"a1","action":"collect_now","issued_at":100,"expires_at":200,"payload":{}}`
	var cmd Command
	if err := StrictDecode([]byte(good), &cmd); err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(good, `"payload":{}`, `"payload":{},"extra":true`, 1)
	if err := StrictDecode([]byte(bad), &cmd); err == nil {
		t.Fatal("accepted unknown field")
	}
}

func TestStrictDecodeRejectsInvalidCommandInput(t *testing.T) {
	good := `{"control_version":"1","type":"command","command_id":"c1","sequence":1,"agent_id":"a1","action":"collect_now","issued_at":100,"expires_at":200,"payload":{}}`
	tests := []struct {
		name string
		data string
	}{
		{name: "unknown action", data: strings.Replace(good, "collect_now", "shell", 1)},
		{name: "unknown type", data: strings.Replace(good, `"command"`, `"unknown"`, 1)},
		{name: "trailing JSON", data: good + ` {}`},
		{name: "message exceeds limit", data: good + strings.Repeat(" ", MaxMessageBytes+1-len(good))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cmd Command
			if err := StrictDecode([]byte(tt.data), &cmd); err == nil {
				t.Fatal("accepted invalid command input")
			}
		})
	}
}

func TestTTLGivesEachActionItsBoundedLifetime(t *testing.T) {
	tests := []struct {
		action Action
		want   string
	}{
		{action: CollectNow, want: "5m0s"},
		{action: ReloadConfig, want: "30m0s"},
		{action: SelfCheck, want: "30m0s"},
		{action: Upgrade, want: "24h0m0s"},
		{action: Action("unknown"), want: "0s"},
	}
	for _, tt := range tests {
		t.Run(string(tt.action), func(t *testing.T) {
			if got := TTL(tt.action).String(); got != tt.want {
				t.Fatalf("TTL(%q) = %s, want %s", tt.action, got, tt.want)
			}
		})
	}
}
