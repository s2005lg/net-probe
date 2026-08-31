package wsclient

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func pipeConn(t *testing.T) (*Conn, net.Conn) {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	return &Conn{rwc: client, reader: bufio.NewReader(client), limit: 1 << 20, pongs: make(map[string]chan struct{})}, server
}

func TestWriteMasksClientFrame(t *testing.T) {
	connection, server := pipeConn(t)
	done := make(chan error, 1)
	go func() { done <- connection.Write(context.Background(), MessageText, []byte("hello")) }()

	opcode, payload, masked, err := readTestFrame(server)
	if err != nil {
		t.Fatal(err)
	}
	if opcode != 1 || string(payload) != "hello" || !masked {
		t.Fatalf("opcode=%d payload=%q masked=%v", opcode, payload, masked)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestReadHandlesPingAndFragmentedText(t *testing.T) {
	connection, server := pipeConn(t)
	serverDone := make(chan error, 1)
	go func() {
		if err := writeTestServerFrame(server, true, 9, []byte("health")); err != nil {
			serverDone <- err
			return
		}
		opcode, payload, masked, err := readTestFrame(server)
		if err != nil {
			serverDone <- err
			return
		}
		if opcode != 10 || string(payload) != "health" || !masked {
			serverDone <- errors.New("pong frame did not echo the masked ping payload")
			return
		}
		if err := writeTestServerFrame(server, false, 1, []byte("hel")); err != nil {
			serverDone <- err
			return
		}
		serverDone <- writeTestServerFrame(server, true, 0, []byte("lo"))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	messageType, payload, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if messageType != MessageText || string(payload) != "hello" {
		t.Fatalf("messageType=%d payload=%q", messageType, payload)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestReadRejectsFrameAboveLimit(t *testing.T) {
	connection, server := pipeConn(t)
	connection.SetReadLimit(10)
	go func() { _ = writeTestServerFrame(server, true, 1, make([]byte, 11)) }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, _, err := connection.Read(ctx); err == nil {
		t.Fatal("oversized frame was accepted")
	}
}

func writeTestServerFrame(writer io.Writer, fin bool, opcode byte, payload []byte) error {
	first := opcode
	if fin {
		first |= 0x80
	}
	header := []byte{first, byte(len(payload))}
	if len(payload) >= 126 {
		return errors.New("test helper only supports short frames")
	}
	if _, err := writer.Write(append(header, payload...)); err != nil {
		return err
	}
	return nil
}

func readTestFrame(reader io.Reader) (byte, []byte, bool, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		return 0, nil, false, err
	}
	length := int(header[1] & 0x7f)
	if length == 126 {
		extended := make([]byte, 2)
		if _, err := io.ReadFull(reader, extended); err != nil {
			return 0, nil, false, err
		}
		length = int(binary.BigEndian.Uint16(extended))
	}
	masked := header[1]&0x80 != 0
	mask := make([]byte, 4)
	if masked {
		if _, err := io.ReadFull(reader, mask); err != nil {
			return 0, nil, false, err
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return 0, nil, false, err
	}
	if masked {
		for index := range payload {
			payload[index] ^= mask[index&3]
		}
	}
	return header[0] & 0x0f, payload, masked, nil
}
