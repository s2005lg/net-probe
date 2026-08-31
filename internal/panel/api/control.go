package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/s2005lg/net-probe/internal/controlproto"
	panelcontrol "github.com/s2005lg/net-probe/internal/panel/control"
)

func (s *Server) ConfigureControlHub(hub *panelcontrol.Hub) {
	if hub != nil {
		s.controlHub = hub
	}
}

type webSocketSession struct {
	id       string
	conn     *websocket.Conn
	outbound chan []byte
	cancel   context.CancelFunc
	mu       sync.Mutex
	reason   string
	close    sync.Once
}

func (s *webSocketSession) SessionID() string       { return s.id }
func (s *webSocketSession) Outbound() chan<- []byte { return s.outbound }

func (s *webSocketSession) Close(reason string) {
	s.close.Do(func() {
		s.mu.Lock()
		s.reason = reason
		s.mu.Unlock()
		s.cancel()
		_ = s.conn.Close(websocket.StatusPolicyViolation, reason)
	})
}

func (s *webSocketSession) Reason(fallback string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reason != "" {
		return s.reason
	}
	return fallback
}

func (s *Server) handleControl(w http.ResponseWriter, r *http.Request, identity AgentIdentity) {
	if s.controlHub == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"code": "unavailable"}})
		return
	}
	connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	connection.SetReadLimit(controlproto.MaxMessageBytes)
	defer connection.CloseNow()

	helloContext, cancelHello := context.WithTimeout(r.Context(), 10*time.Second)
	messageType, body, err := connection.Read(helloContext)
	cancelHello()
	if err != nil || messageType != websocket.MessageText {
		_ = connection.Close(websocket.StatusPolicyViolation, "hello_required")
		return
	}
	var hello controlproto.Hello
	if err := controlproto.StrictDecode(body, &hello); err != nil || !validHello(hello, identity) {
		_ = connection.Close(websocket.StatusPolicyViolation, "invalid_hello")
		return
	}
	sessionID, err := randomUUID()
	if err != nil {
		_ = connection.Close(websocket.StatusInternalError, "session_error")
		return
	}
	sessionContext, cancelSession := context.WithCancel(r.Context())
	session := &webSocketSession{
		id: sessionID, conn: connection, outbound: make(chan []byte, s.controlHub.QueueSize()), cancel: cancelSession,
	}
	if err := s.controlHub.Register(identity.AgentID, session); err != nil {
		cancelSession()
		_ = connection.Close(websocket.StatusTryAgainLater, "capacity")
		return
	}
	disconnectReason := "closed"
	defer func() {
		cancelSession()
		s.controlHub.Unregister(identity.AgentID, sessionID)
		now := time.Now().Unix()
		_, _ = s.db.Exec(`UPDATE agent_identities SET last_disconnected_at=?,disconnect_reason=?,updated_at=? WHERE agent_id=? AND cert_serial=?`,
			now, session.Reason(disconnectReason), now, identity.AgentID, identity.Serial)
	}()

	capabilities, _ := json.Marshal(hello.Capabilities)
	now := time.Now().Unix()
	if _, err := s.db.Exec(`UPDATE agent_identities SET last_connected_at=?,last_heartbeat_at=?,disconnect_reason='',agent_version=?,os=?,arch=?,capabilities_json=?,updated_at=? WHERE agent_id=? AND cert_serial=?`,
		now, now, hello.AgentVersion, hello.OS, hello.Arch, string(capabilities), now, identity.AgentID, identity.Serial); err != nil {
		disconnectReason = "db_error"
		_ = connection.Close(websocket.StatusInternalError, disconnectReason)
		return
	}
	welcome := controlproto.Welcome{
		ControlVersion: controlproto.Version, Type: "welcome", SessionID: sessionID, ServerTime: now,
		HeartbeatSeconds: int(controlproto.HeartbeatInterval / time.Second), OfflineSeconds: int(controlproto.OfflineTimeout / time.Second),
		MaxMessageBytes: controlproto.MaxMessageBytes,
	}
	welcomeBody, _ := json.Marshal(welcome)
	if err := connection.Write(sessionContext, websocket.MessageText, welcomeBody); err != nil {
		disconnectReason = "welcome_failed"
		return
	}

	writerDone := make(chan error, 1)
	go session.writeLoop(sessionContext, writerDone)
	windowStarted := time.Now()
	messages := 0
	for {
		readContext, cancelRead := context.WithTimeout(sessionContext, controlproto.OfflineTimeout)
		messageType, body, err = connection.Read(readContext)
		cancelRead()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				disconnectReason = "heartbeat_timeout"
			} else {
				disconnectReason = "transport_closed"
			}
			return
		}
		if messageType != websocket.MessageText {
			disconnectReason = "invalid_message_type"
			_ = connection.Close(websocket.StatusUnsupportedData, disconnectReason)
			return
		}
		if time.Since(windowStarted) >= time.Minute {
			windowStarted, messages = time.Now(), 0
		}
		messages++
		if messages > s.cfg.Control.MessageRate {
			disconnectReason = "rate_limited"
			_ = connection.Close(websocket.StatusPolicyViolation, disconnectReason)
			return
		}
		var heartbeat controlproto.Heartbeat
		if err := controlproto.StrictDecode(body, &heartbeat); err != nil || !validHeartbeat(heartbeat) {
			disconnectReason = "invalid_heartbeat"
			_ = connection.Close(websocket.StatusPolicyViolation, disconnectReason)
			return
		}
		now = time.Now().Unix()
		result, err := s.db.Exec(`UPDATE agent_identities SET last_heartbeat_at=?,agent_version=?,updated_at=? WHERE agent_id=? AND cert_serial=? AND revoked_at=0`,
			now, heartbeat.AgentVersion, now, identity.AgentID, identity.Serial)
		if err != nil {
			disconnectReason = "db_error"
			_ = connection.Close(websocket.StatusInternalError, disconnectReason)
			return
		}
		if updated, err := result.RowsAffected(); err != nil || updated != 1 {
			disconnectReason = "revoked"
			_ = connection.Close(websocket.StatusPolicyViolation, disconnectReason)
			return
		}
		select {
		case err := <-writerDone:
			if err != nil {
				disconnectReason = "write_failed"
			}
			return
		default:
		}
	}
}

func (s *webSocketSession) writeLoop(ctx context.Context, done chan<- error) {
	ticker := time.NewTicker(controlproto.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			done <- nil
			return
		case message := <-s.outbound:
			if err := s.conn.Write(ctx, websocket.MessageText, message); err != nil {
				done <- err
				return
			}
		case <-ticker.C:
			pingContext, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := s.conn.Ping(pingContext)
			cancel()
			if err != nil {
				done <- err
				return
			}
		}
	}
}

func validHello(hello controlproto.Hello, identity AgentIdentity) bool {
	return hello.AgentID == identity.AgentID && hello.NodeID == identity.NodeID && hello.BootID != "" &&
		len(hello.BootID) <= 256 && len(hello.AgentVersion) <= 128 && len(hello.OS) <= 64 && len(hello.Arch) <= 64 && len(hello.Capabilities) <= 4
}

func validHeartbeat(heartbeat controlproto.Heartbeat) bool {
	return len(heartbeat.AgentVersion) <= 128 && len(heartbeat.LastReportCode) <= 128 &&
		len(heartbeat.CurrentCommandID) <= 128 && heartbeat.UptimeSeconds >= 0 && heartbeat.OutboxDepth >= 0 && heartbeat.OutboxDepth <= 120
}
