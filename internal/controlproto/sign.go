package controlproto

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
)

// SigningBytes returns the canonical bytes covered by a command signature.
// Each scalar is length-prefixed so no pair of adjacent values can be
// reinterpreted as a different envelope. Payload bytes are represented by their
// exact SHA-256 digest, preserving the distinction between semantically equal
// but differently encoded JSON.
func SigningBytes(cmd Command) []byte {
	payloadHash := sha256.Sum256(cmd.Payload)

	buf := make([]byte, 0, len(cmd.ControlVersion)+len(cmd.Type)+len(cmd.CommandID)+len(cmd.AgentID)+len(cmd.Action)+96)
	buf = appendLengthPrefixed(buf, []byte(cmd.ControlVersion))
	buf = appendLengthPrefixed(buf, []byte(cmd.Type))
	buf = appendLengthPrefixed(buf, []byte(cmd.CommandID))
	buf = appendLengthPrefixed(buf, uint64Bytes(cmd.Sequence))
	buf = appendLengthPrefixed(buf, []byte(cmd.AgentID))
	buf = appendLengthPrefixed(buf, []byte(cmd.Action))
	buf = appendLengthPrefixed(buf, int64Bytes(cmd.IssuedAt))
	buf = appendLengthPrefixed(buf, int64Bytes(cmd.ExpiresAt))
	return appendLengthPrefixed(buf, payloadHash[:])
}

// SignCommand signs the complete command envelope using an Ed25519 private
// key and stores the standard-base64 signature on cmd.
func SignCommand(privateKey ed25519.PrivateKey, cmd *Command) error {
	if cmd == nil {
		return fmt.Errorf("sign command: nil command")
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("sign command: invalid Ed25519 private key length %d", len(privateKey))
	}
	cmd.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, SigningBytes(*cmd)))
	return nil
}

// VerifyCommand verifies that the signature covers the complete command
// envelope using an Ed25519 public key.
func VerifyCommand(publicKey ed25519.PublicKey, cmd Command) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("verify command: invalid Ed25519 public key length %d", len(publicKey))
	}
	signature, err := base64.StdEncoding.DecodeString(cmd.Signature)
	if err != nil {
		return fmt.Errorf("verify command: decode signature: %w", err)
	}
	if len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("verify command: invalid Ed25519 signature length %d", len(signature))
	}
	if !ed25519.Verify(publicKey, SigningBytes(cmd), signature) {
		return fmt.Errorf("verify command: invalid signature")
	}
	return nil
}

func appendLengthPrefixed(dst, value []byte) []byte {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	dst = append(dst, length[:]...)
	return append(dst, value...)
}

func uint64Bytes(value uint64) []byte {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	return encoded[:]
}

func int64Bytes(value int64) []byte {
	return uint64Bytes(uint64(value))
}
