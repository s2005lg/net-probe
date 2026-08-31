// Package wsclient implements the small RFC 6455 client subset used by the
// resident Agent. It deliberately does not negotiate compression or expose
// server APIs so those features are not linked into the Agent binary.
package wsclient

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

type MessageType byte

const (
	MessageText   MessageType = 1
	MessageBinary MessageType = 2

	StatusNormalClosure uint16 = 1000
)

const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

type Conn struct {
	rwc      io.ReadWriteCloser
	reader   *bufio.Reader
	writeMu  sync.Mutex
	closeOne sync.Once
	limit    int64

	pongMu sync.Mutex
	pongs  map[string]chan struct{}
}

// Dial performs an RFC 6455 HTTP upgrade. The caller owns a successful Conn.
// A non-101 HTTP response is returned so authentication failures can be
// classified without exposing its response body in error text.
func Dial(ctx context.Context, address string, client *http.Client) (*Conn, *http.Response, error) {
	parsed, err := url.Parse(address)
	if err != nil {
		return nil, nil, errors.New("parse WebSocket URL")
	}
	switch parsed.Scheme {
	case "wss":
		parsed.Scheme = "https"
	case "ws":
		parsed.Scheme = "http"
	default:
		return nil, nil, errors.New("WebSocket URL must use ws or wss")
	}
	keyBytes := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, keyBytes); err != nil {
		return nil, nil, errors.New("generate WebSocket key")
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, nil, errors.New("create WebSocket request")
	}
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Sec-WebSocket-Version", "13")
	request.Header.Set("Sec-WebSocket-Key", key)
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, response, err
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		_ = response.Body.Close()
		return nil, response, fmt.Errorf("WebSocket upgrade returned HTTP %d", response.StatusCode)
	}
	acceptSum := sha1.Sum([]byte(key + websocketGUID))
	expectedAccept := base64.StdEncoding.EncodeToString(acceptSum[:])
	if !headerHasToken(response.Header, "Connection", "upgrade") ||
		!headerHasToken(response.Header, "Upgrade", "websocket") ||
		response.Header.Get("Sec-WebSocket-Accept") != expectedAccept ||
		response.Header.Get("Sec-WebSocket-Extensions") != "" {
		_ = response.Body.Close()
		return nil, response, errors.New("invalid WebSocket upgrade response")
	}
	rwc, ok := response.Body.(io.ReadWriteCloser)
	if !ok {
		_ = response.Body.Close()
		return nil, response, errors.New("WebSocket response is not writable")
	}
	response.Body = nil
	return &Conn{rwc: rwc, reader: bufio.NewReader(rwc), limit: 1 << 20, pongs: make(map[string]chan struct{})}, response, nil
}

func headerHasToken(header http.Header, name, token string) bool {
	for _, value := range header.Values(name) {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

func (c *Conn) SetReadLimit(limit int64) {
	if limit > 0 {
		c.limit = limit
	}
}

func (c *Conn) Read(ctx context.Context) (MessageType, []byte, error) {
	var message []byte
	var messageType MessageType
	for {
		fin, opcode, payload, err := c.readFrame(ctx)
		if err != nil {
			return 0, nil, err
		}
		switch opcode {
		case 0x8:
			_ = c.writeFrame(context.Background(), 0x8, payload)
			c.CloseNow()
			return 0, nil, io.EOF
		case 0x9:
			if err := c.writeFrame(ctx, 0xA, payload); err != nil {
				return 0, nil, err
			}
			continue
		case 0xA:
			c.resolvePong(string(payload))
			continue
		case 0x1, 0x2:
			if messageType != 0 {
				return 0, nil, errors.New("new WebSocket message before final continuation")
			}
			messageType = MessageType(opcode)
		case 0x0:
			if messageType == 0 {
				return 0, nil, errors.New("unexpected WebSocket continuation")
			}
		default:
			return 0, nil, errors.New("unsupported WebSocket opcode")
		}
		if int64(len(message))+int64(len(payload)) > c.limit {
			return 0, nil, errors.New("WebSocket message exceeds read limit")
		}
		message = append(message, payload...)
		if fin {
			return messageType, message, nil
		}
	}
}

func (c *Conn) readFrame(ctx context.Context) (bool, byte, []byte, error) {
	stop := context.AfterFunc(ctx, c.CloseNow)
	defer stop()
	header := make([]byte, 2)
	if _, err := io.ReadFull(c.reader, header); err != nil {
		return false, 0, nil, readError(ctx, err)
	}
	if header[0]&0x70 != 0 || header[1]&0x80 != 0 {
		return false, 0, nil, errors.New("invalid WebSocket frame flags")
	}
	fin, opcode := header[0]&0x80 != 0, header[0]&0x0f
	length := uint64(header[1] & 0x7f)
	switch length {
	case 126:
		extended := make([]byte, 2)
		if _, err := io.ReadFull(c.reader, extended); err != nil {
			return false, 0, nil, readError(ctx, err)
		}
		length = uint64(binary.BigEndian.Uint16(extended))
	case 127:
		extended := make([]byte, 8)
		if _, err := io.ReadFull(c.reader, extended); err != nil {
			return false, 0, nil, readError(ctx, err)
		}
		length = binary.BigEndian.Uint64(extended)
		if length>>63 != 0 {
			return false, 0, nil, errors.New("invalid WebSocket frame length")
		}
	}
	if opcode >= 0x8 && (!fin || length > 125) {
		return false, 0, nil, errors.New("invalid WebSocket control frame")
	}
	if length > uint64(c.limit) {
		return false, 0, nil, errors.New("WebSocket frame exceeds read limit")
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(c.reader, payload); err != nil {
		return false, 0, nil, readError(ctx, err)
	}
	return fin, opcode, payload, nil
}

func readError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

func (c *Conn) Write(ctx context.Context, messageType MessageType, payload []byte) error {
	if messageType != MessageText && messageType != MessageBinary {
		return errors.New("invalid WebSocket message type")
	}
	return c.writeFrame(ctx, byte(messageType), payload)
}

func (c *Conn) Ping(ctx context.Context) error {
	tokenBytes := make([]byte, 8)
	if _, err := io.ReadFull(rand.Reader, tokenBytes); err != nil {
		return err
	}
	token := string(tokenBytes)
	resolved := make(chan struct{})
	c.pongMu.Lock()
	c.pongs[token] = resolved
	c.pongMu.Unlock()
	defer func() {
		c.pongMu.Lock()
		delete(c.pongs, token)
		c.pongMu.Unlock()
	}()
	if err := c.writeFrame(ctx, 0x9, tokenBytes); err != nil {
		return err
	}
	select {
	case <-resolved:
		return nil
	case <-ctx.Done():
		c.CloseNow()
		return ctx.Err()
	}
}

func (c *Conn) resolvePong(token string) {
	c.pongMu.Lock()
	resolved := c.pongs[token]
	if resolved != nil {
		delete(c.pongs, token)
		close(resolved)
	}
	c.pongMu.Unlock()
}

func (c *Conn) writeFrame(ctx context.Context, opcode byte, payload []byte) error {
	if opcode >= 0x8 && len(payload) > 125 {
		return errors.New("WebSocket control payload is too large")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	stop := context.AfterFunc(ctx, c.CloseNow)
	defer stop()
	mask := make([]byte, 4)
	if _, err := io.ReadFull(rand.Reader, mask); err != nil {
		return err
	}
	header := []byte{0x80 | opcode, 0x80}
	switch length := len(payload); {
	case length < 126:
		header[1] |= byte(length)
	case uint64(length) <= uint64(^uint16(0)):
		header[1] |= 126
		extended := make([]byte, 2)
		binary.BigEndian.PutUint16(extended, uint16(length))
		header = append(header, extended...)
	default:
		header[1] |= 127
		extended := make([]byte, 8)
		binary.BigEndian.PutUint64(extended, uint64(length))
		header = append(header, extended...)
	}
	header = append(header, mask...)
	masked := make([]byte, len(payload))
	for index := range payload {
		masked[index] = payload[index] ^ mask[index&3]
	}
	if _, err := c.rwc.Write(append(header, masked...)); err != nil {
		return readError(ctx, err)
	}
	return nil
}

func (c *Conn) Close(status uint16, reason string) error {
	payload := make([]byte, 2, 2+len(reason))
	binary.BigEndian.PutUint16(payload, status)
	payload = append(payload, reason...)
	err := c.writeFrame(context.Background(), 0x8, payload)
	c.CloseNow()
	return err
}

func (c *Conn) CloseNow() {
	c.closeOne.Do(func() { _ = c.rwc.Close() })
}
