package controlproto

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"
)

func TestCommandSignatureCoversEnvelope(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cmd := Command{
		ControlVersion: Version,
		Type:           "command",
		CommandID:      "c1",
		Sequence:       7,
		AgentID:        "a1",
		Action:         CollectNow,
		IssuedAt:       10,
		ExpiresAt:      20,
		Payload:        json.RawMessage(`{}`),
	}
	if err := SignCommand(priv, &cmd); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCommand(pub, cmd); err != nil {
		t.Fatal(err)
	}
	cmd.AgentID = "a2"
	if err := VerifyCommand(pub, cmd); err == nil {
		t.Fatal("accepted mutation")
	}
}

func TestCommandSignatureRejectsEveryEnvelopeMutation(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	base := Command{
		ControlVersion: Version,
		Type:           "command",
		CommandID:      "c1",
		Sequence:       7,
		AgentID:        "a1",
		Action:         CollectNow,
		IssuedAt:       10,
		ExpiresAt:      20,
		Payload:        json.RawMessage(`{"requested":true}`),
	}
	if err := SignCommand(priv, &base); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*Command)
	}{
		{name: "control version", mutate: func(cmd *Command) { cmd.ControlVersion = "2" }},
		{name: "type", mutate: func(cmd *Command) { cmd.Type = "welcome" }},
		{name: "command ID", mutate: func(cmd *Command) { cmd.CommandID = "c2" }},
		{name: "sequence", mutate: func(cmd *Command) { cmd.Sequence++ }},
		{name: "agent ID", mutate: func(cmd *Command) { cmd.AgentID = "a2" }},
		{name: "action", mutate: func(cmd *Command) { cmd.Action = ReloadConfig }},
		{name: "issued at", mutate: func(cmd *Command) { cmd.IssuedAt++ }},
		{name: "expires at", mutate: func(cmd *Command) { cmd.ExpiresAt++ }},
		{name: "payload", mutate: func(cmd *Command) { cmd.Payload = json.RawMessage(`{"requested":false}`) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := base
			tt.mutate(&cmd)
			if err := VerifyCommand(pub, cmd); err == nil {
				t.Fatal("accepted mutated command")
			}
		})
	}
}
