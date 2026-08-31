package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/s2005lg/net-probe/internal/controlproto"
)

const permanentControlRetry = 15 * time.Minute

type ControlOptions struct {
	PanelURL         string
	Identity         *Identity
	NodeID           string
	Version          string
	Capabilities     []controlproto.Action
	HighestCompleted func() uint64
	OutboxDepth      func() int
	LastReportCode   func() string
	CurrentCommandID func() string
	HandleCommand    func(context.Context, controlproto.Command) controlproto.CommandResult
	OnWelcome        func()
}

type ControlClient struct {
	options           ControlOptions
	controlURL        string
	bootID            string
	startedAt         time.Time
	heartbeatOverride time.Duration
	transientBackoff  func(int) time.Duration
	onHeartbeat       func(controlproto.Heartbeat)
}

type permanentControlError struct{ err error }

func (e permanentControlError) Error() string { return e.err.Error() }
func (e permanentControlError) Unwrap() error { return e.err }

func NewControlClient(options ControlOptions) (*ControlClient, error) {
	if options.Identity == nil || options.Identity.TLSConfig == nil || options.Identity.AgentID == "" || options.NodeID == "" {
		return nil, errors.New("control client requires an enrolled identity and Node ID")
	}
	parsed, err := url.Parse(options.PanelURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return nil, errors.New("control Panel URL must use HTTPS")
	}
	parsed.Scheme = "wss"
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/api/v1/agents/control"
	parsed.RawPath, parsed.RawQuery, parsed.Fragment = "", "", ""
	bootID, err := readBootID()
	if err != nil {
		return nil, err
	}
	client := &ControlClient{options: options, controlURL: parsed.String(), bootID: bootID, startedAt: time.Now()}
	client.transientBackoff = transientControlDelay
	return client, nil
}

func (c *ControlClient) Run(ctx context.Context) error {
	failures := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		err := c.runSession(ctx)
		if ctx.Err() != nil {
			return nil
		}
		failures++
		delay := c.transientBackoff(failures)
		var permanent permanentControlError
		if errors.As(err, &permanent) {
			delay = permanentControlRetry
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil
		case <-timer.C:
		}
	}
}

func (c *ControlClient) runSession(ctx context.Context) error {
	httpClient := &http.Client{
		Timeout:   20 * time.Second,
		Transport: &http.Transport{TLSClientConfig: c.options.Identity.TLSConfig.Clone()},
	}
	connection, response, err := websocket.Dial(ctx, c.controlURL, &websocket.DialOptions{
		HTTPClient: httpClient, CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		if response != nil && (response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden) {
			return permanentControlError{err: errors.New("control identity rejected")}
		}
		return fmt.Errorf("dial control channel: %w", err)
	}
	connection.SetReadLimit(controlproto.MaxMessageBytes)
	defer connection.CloseNow()
	hello := controlproto.Hello{
		ControlVersion: controlproto.Version, Type: "hello", AgentID: c.options.Identity.AgentID,
		NodeID: c.options.NodeID, AgentVersion: c.options.Version, OS: runtime.GOOS, Arch: runtime.GOARCH,
		Capabilities: append([]controlproto.Action(nil), c.options.Capabilities...), BootID: c.bootID,
	}
	if c.options.HighestCompleted != nil {
		hello.HighestCompleted = c.options.HighestCompleted()
	}
	helloBody, err := json.Marshal(hello)
	if err != nil {
		return err
	}
	if err := connection.Write(ctx, websocket.MessageText, helloBody); err != nil {
		return fmt.Errorf("write control hello: %w", err)
	}
	welcomeContext, cancelWelcome := context.WithTimeout(ctx, 15*time.Second)
	messageType, body, err := connection.Read(welcomeContext)
	cancelWelcome()
	if err != nil || messageType != websocket.MessageText {
		return permanentControlError{err: errors.New("control welcome missing")}
	}
	var welcome controlproto.Welcome
	if err := controlproto.StrictDecode(body, &welcome); err != nil || !validWelcome(welcome) {
		return permanentControlError{err: errors.New("control welcome invalid")}
	}
	if c.options.OnWelcome != nil {
		c.options.OnWelcome()
	}
	heartbeatInterval := time.Duration(welcome.HeartbeatSeconds) * time.Second
	if c.heartbeatOverride > 0 {
		heartbeatInterval = c.heartbeatOverride
	}
	heartbeatTicker := time.NewTicker(heartbeatInterval)
	defer heartbeatTicker.Stop()
	pingTicker := time.NewTicker(heartbeatInterval)
	defer pingTicker.Stop()
	sessionContext, cancelSession := context.WithCancel(ctx)
	defer cancelSession()
	var commandQueue chan<- controlproto.Command
	var commandResults <-chan controlproto.CommandResult
	if c.options.HandleCommand != nil {
		commandQueue, commandResults = startCommandWorker(sessionContext, 32, c.options.HandleCommand)
	}
	type inboundMessage struct {
		messageType websocket.MessageType
		body        []byte
		err         error
	}
	inbound := make(chan inboundMessage, 1)
	readContext, cancelRead := context.WithCancel(ctx)
	defer cancelRead()
	go func() {
		for {
			messageType, body, err := connection.Read(readContext)
			select {
			case inbound <- inboundMessage{messageType: messageType, body: body, err: err}:
			case <-readContext.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			_ = connection.Close(websocket.StatusNormalClosure, "shutdown")
			return nil
		case event := <-inbound:
			if event.err != nil {
				return fmt.Errorf("read control channel: %w", event.err)
			}
			if event.messageType != websocket.MessageText {
				return permanentControlError{err: errors.New("unsupported control message type")}
			}
			var command controlproto.Command
			if err := controlproto.StrictDecode(event.body, &command); err != nil {
				return permanentControlError{err: errors.New("invalid control command")}
			}
			if c.options.HandleCommand == nil {
				return permanentControlError{err: errors.New("control command executor unavailable")}
			}
			select {
			case commandQueue <- command:
			default:
				if err := writeCommandResult(ctx, connection, failedCommandResult(command, "command_backpressure")); err != nil {
					return err
				}
			}
		case result := <-commandResults:
			if err := writeCommandResult(ctx, connection, result); err != nil {
				return err
			}
		case <-heartbeatTicker.C:
			heartbeat := c.heartbeat()
			if c.onHeartbeat != nil {
				c.onHeartbeat(heartbeat)
			}
			body, _ := json.Marshal(heartbeat)
			if err := connection.Write(ctx, websocket.MessageText, body); err != nil {
				return fmt.Errorf("write control heartbeat: %w", err)
			}
		case <-pingTicker.C:
			pingContext, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := connection.Ping(pingContext)
			cancel()
			if err != nil {
				return fmt.Errorf("control ping: %w", err)
			}
		}
	}
}

func startCommandWorker(ctx context.Context, queueSize int, handler func(context.Context, controlproto.Command) controlproto.CommandResult) (chan<- controlproto.Command, <-chan controlproto.CommandResult) {
	if queueSize <= 0 || queueSize > 32 {
		queueSize = 32
	}
	queue := make(chan controlproto.Command, queueSize)
	results := make(chan controlproto.CommandResult, queueSize)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case command := <-queue:
				result := handler(ctx, command)
				select {
				case results <- result:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return queue, results
}

func writeCommandResult(ctx context.Context, connection *websocket.Conn, result controlproto.CommandResult) error {
	resultBody, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if err := connection.Write(ctx, websocket.MessageText, resultBody); err != nil {
		return fmt.Errorf("write command result: %w", err)
	}
	return nil
}

func (c *ControlClient) heartbeat() controlproto.Heartbeat {
	heartbeat := controlproto.Heartbeat{
		ControlVersion: controlproto.Version, Type: "heartbeat", AgentVersion: c.options.Version,
		UptimeSeconds: int64(time.Since(c.startedAt) / time.Second),
	}
	if c.options.OutboxDepth != nil {
		heartbeat.OutboxDepth = c.options.OutboxDepth()
	}
	if c.options.LastReportCode != nil {
		heartbeat.LastReportCode = c.options.LastReportCode()
	}
	if c.options.CurrentCommandID != nil {
		heartbeat.CurrentCommandID = c.options.CurrentCommandID()
	}
	return heartbeat
}

func validWelcome(welcome controlproto.Welcome) bool {
	return welcome.SessionID != "" && welcome.HeartbeatSeconds > 0 && welcome.OfflineSeconds >= welcome.HeartbeatSeconds &&
		welcome.MaxMessageBytes > 0 && welcome.MaxMessageBytes <= controlproto.MaxMessageBytes
}

func readBootID() (string, error) {
	if body, err := os.ReadFile("/proc/sys/kernel/random/boot_id"); err == nil {
		if value := strings.TrimSpace(string(body)); value != "" && len(value) <= 256 {
			return value, nil
		}
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return "process-" + hex.EncodeToString(random), nil
}

func transientControlDelay(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	base := time.Second
	for i := 1; i < failures && base < 60*time.Second; i++ {
		base *= 2
	}
	if base > 60*time.Second {
		base = 60 * time.Second
	}
	random := []byte{0}
	_, _ = rand.Read(random)
	percent := int(random[0])%41 - 20
	return base + time.Duration(percent)*base/100
}
