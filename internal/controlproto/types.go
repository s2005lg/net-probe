package controlproto

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

const (
	Version           = "1"
	MaxMessageBytes   = 64 << 10
	HeartbeatInterval = 30 * time.Second
	OfflineTimeout    = 90 * time.Second
)

type Action string

const (
	CollectNow   Action = "collect_now"
	ReloadConfig Action = "reload_config"
	SelfCheck    Action = "self_check"
	Upgrade      Action = "upgrade"
)

type Command struct {
	ControlVersion string          `json:"control_version"`
	Type           string          `json:"type"`
	CommandID      string          `json:"command_id"`
	Sequence       uint64          `json:"sequence"`
	AgentID        string          `json:"agent_id"`
	Action         Action          `json:"action"`
	IssuedAt       int64           `json:"issued_at"`
	ExpiresAt      int64           `json:"expires_at"`
	Payload        json.RawMessage `json:"payload"`
	Signature      string          `json:"signature,omitempty"`
}

type Hello struct {
	ControlVersion   string   `json:"control_version"`
	Type             string   `json:"type"`
	AgentID          string   `json:"agent_id"`
	NodeID           string   `json:"node_id"`
	AgentVersion     string   `json:"agent_version"`
	OS               string   `json:"os"`
	Arch             string   `json:"arch"`
	Capabilities     []Action `json:"capabilities"`
	BootID           string   `json:"boot_id"`
	HighestCompleted uint64   `json:"highest_completed"`
}

type Welcome struct {
	ControlVersion   string `json:"control_version"`
	Type             string `json:"type"`
	SessionID        string `json:"session_id"`
	ServerTime       int64  `json:"server_time"`
	HeartbeatSeconds int    `json:"heartbeat_seconds"`
	OfflineSeconds   int    `json:"offline_seconds"`
	MaxMessageBytes  int    `json:"max_message_bytes"`
	PanelVersion     string `json:"panel_version"`
	AgentVersion     string `json:"agent_version"`
	BootID           string `json:"boot_id"`
	NextSequence     uint64 `json:"next_sequence,omitempty"`
}

type Heartbeat struct {
	ControlVersion   string `json:"control_version"`
	Type             string `json:"type"`
	AgentVersion     string `json:"agent_version"`
	UptimeSeconds    int64  `json:"uptime_seconds"`
	LastReportCode   string `json:"last_report_code"`
	CurrentCommandID string `json:"current_command_id,omitempty"`
	OutboxDepth      int    `json:"outbox_depth"`
}

type CommandResult struct {
	ControlVersion string          `json:"control_version"`
	Type           string          `json:"type"`
	CommandID      string          `json:"command_id"`
	Sequence       uint64          `json:"sequence"`
	State          string          `json:"state"`
	Code           string          `json:"code"`
	Data           json.RawMessage `json:"data,omitempty"`
}

// StrictDecode decodes exactly one supported protocol object and validates its
// version, message type, and any declared actions.
func StrictDecode(data []byte, dst any) error {
	if len(data) > MaxMessageBytes {
		return fmt.Errorf("control message exceeds %d bytes", MaxMessageBytes)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("decode control message: %w", err)
	}
	if err := ensureEOF(dec); err != nil {
		return err
	}

	switch msg := dst.(type) {
	case *Command:
		if err := validateHeader(msg.ControlVersion, msg.Type, "command"); err != nil {
			return err
		}
		if !validAction(msg.Action) {
			return fmt.Errorf("unknown command action %q", msg.Action)
		}
	case *Hello:
		if err := validateHeader(msg.ControlVersion, msg.Type, "hello"); err != nil {
			return err
		}
		for _, action := range msg.Capabilities {
			if !validAction(action) {
				return fmt.Errorf("unknown capability %q", action)
			}
		}
	case *Welcome:
		if err := validateHeader(msg.ControlVersion, msg.Type, "welcome"); err != nil {
			return err
		}
	case *Heartbeat:
		if err := validateHeader(msg.ControlVersion, msg.Type, "heartbeat"); err != nil {
			return err
		}
	case *CommandResult:
		if err := validateHeader(msg.ControlVersion, msg.Type, "command_result"); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported control message destination %T", dst)
	}
	return nil
}

// StrictDecodePayload decodes one bounded JSON payload and rejects unknown
// fields and trailing values. Action-specific validation remains with callers.
func StrictDecodePayload(data []byte, dst any) error {
	if len(data) == 0 || len(data) > MaxMessageBytes {
		return fmt.Errorf("invalid command payload size")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("decode command payload: %w", err)
	}
	return ensureEOF(dec)
}

func ensureEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("control message contains trailing JSON")
		}
		return fmt.Errorf("decode trailing control message data: %w", err)
	}
	return nil
}

func validateHeader(version, messageType, expectedType string) error {
	if version != Version {
		return fmt.Errorf("unsupported control version %q", version)
	}
	if messageType != expectedType {
		return fmt.Errorf("unexpected control message type %q", messageType)
	}
	return nil
}

func validAction(action Action) bool {
	switch action {
	case CollectNow, ReloadConfig, SelfCheck, Upgrade:
		return true
	default:
		return false
	}
}

// TTL returns the maximum lifetime for commands of action. Unknown actions
// have no valid lifetime.
func TTL(action Action) time.Duration {
	switch action {
	case CollectNow:
		return 5 * time.Minute
	case ReloadConfig, SelfCheck:
		return 30 * time.Minute
	case Upgrade:
		return 24 * time.Hour
	default:
		return 0
	}
}
