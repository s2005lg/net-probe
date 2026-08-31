package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"log"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/s2005lg/net-probe/internal/controlproto"
	panelcontrol "github.com/s2005lg/net-probe/internal/panel/control"
	"github.com/s2005lg/net-probe/internal/panel/pki"
)

type controlFixture struct {
	url    string
	client *http.Client
	hub    *panelcontrol.Hub
	agent  registeredAgent
}

func startControlFixture(t *testing.T) controlFixture {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host := listener.Addr().String()
	manager, err := pki.Ensure(t.TempDir(), "https://"+host)
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	d, cfg := openTestDB(t)
	server := New(d, cfg)
	release, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server.ConfigureAgentPKI(manager, StaticReleasePublicKey(release))
	hub := panelcontrol.NewHub(1000, 32)
	server.ConfigureControlHub(hub)
	agent := registerAgent(t, server, manager, authAgentID, "node-control", time.Now())
	httpServer := NewTLSServer(host, server.Routes(), manager.TLSConfig())
	httpServer.ErrorLog = log.New(io.Discard, "", 0)
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.ServeTLS(listener, manager.ServerCertFile, manager.ServerKeyFile) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
		if err := <-serveErr; err != nil && err != http.ErrServerClosed {
			t.Errorf("serve control fixture: %v", err)
		}
	})
	keyDER, err := x509.MarshalECPrivateKey(agent.key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(agent.certPEM, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	client := tlsHTTPClient(certPool(t, manager.CACertFile), func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
		return &certificate, nil
	})
	return controlFixture{url: "wss://" + host + "/api/v1/agents/control", client: client, hub: hub, agent: agent}
}

func dialControl(t *testing.T, fixture controlFixture) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, fixture.url, &websocket.DialOptions{HTTPClient: fixture.client})
	if err != nil {
		t.Fatal(err)
	}
	return connection
}

func TestControlRequiresMatchingHelloAndReturnsWelcome(t *testing.T) {
	fixture := startControlFixture(t)
	connection := dialControl(t, fixture)
	defer connection.CloseNow()
	hello := controlproto.Hello{
		ControlVersion: controlproto.Version, Type: "hello", AgentID: fixture.agent.identity.AgentID,
		NodeID: fixture.agent.identity.NodeID, AgentVersion: "v1.2.3", OS: "linux", Arch: "amd64",
		Capabilities: []controlproto.Action{controlproto.CollectNow}, BootID: "boot-1",
	}
	body, err := json.Marshal(hello)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := connection.Write(ctx, websocket.MessageText, body); err != nil {
		t.Fatal(err)
	}
	_, response, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var welcome controlproto.Welcome
	if err := controlproto.StrictDecode(response, &welcome); err != nil {
		t.Fatal(err)
	}
	if welcome.SessionID == "" || welcome.HeartbeatSeconds != 30 || !fixture.hub.IsOnline(authAgentID) {
		t.Fatalf("welcome=%+v online=%v", welcome, fixture.hub.IsOnline(authAgentID))
	}
}

func TestControlRejectsFirstNonHelloAndCertificateMismatch(t *testing.T) {
	fixture := startControlFixture(t)
	for _, message := range []any{
		controlproto.Heartbeat{ControlVersion: controlproto.Version, Type: "heartbeat"},
		controlproto.Hello{ControlVersion: controlproto.Version, Type: "hello", AgentID: "123e4567-e89b-42d3-a456-426614174099", NodeID: fixture.agent.identity.NodeID, BootID: "boot"},
	} {
		connection := dialControl(t, fixture)
		body, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := connection.Write(ctx, websocket.MessageText, body); err != nil {
			cancel()
			connection.CloseNow()
			t.Fatal(err)
		}
		_, _, err = connection.Read(ctx)
		cancel()
		connection.CloseNow()
		if err == nil {
			t.Fatalf("accepted invalid first message %T", message)
		}
	}
}
