package update

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/s2005lg/net-probe/internal/controlproto"
)

const (
	PendingRequestFile  = "pending.json"
	ClaimedRequestFile  = "claimed.json"
	RejectedRequestFile = "rejected.json"
	HelperResultFile    = "result.json"
	UpgradeProofFile    = "upgrade-ready.json"
)

type UpdateRequest struct {
	ArtifactBasename string               `json:"artifact_basename"`
	Manifest         Manifest             `json:"manifest"`
	Signature        string               `json:"signature"`
	PreviousVersion  string               `json:"previous_version"`
	AgentID          string               `json:"agent_id"`
	CommandID        string               `json:"command_id"`
	Command          controlproto.Command `json:"command"`
}

type RequestValidationOptions struct {
	UpdatesDir       string
	UpdatesDirFile   *os.File
	PublicKey        ed25519.PublicKey
	CommandPublicKey ed25519.PublicKey
	CurrentVersion   string
	OS               string
	Arch             string
	ControlVersion   string
	AgentID          string
	ExpectedUID      int
	ExpectedGID      int
	Now              time.Time
}

func ValidateUpdateRequest(body []byte, options RequestValidationOptions) (UpdateRequest, []byte, error) {
	request, err := DecodeUpdateRequest(body)
	if err != nil {
		return UpdateRequest{}, nil, err
	}
	if !safeBasename(request.ArtifactBasename) {
		return UpdateRequest{}, nil, errors.New("update artifact basename is invalid")
	}
	if request.PreviousVersion != options.CurrentVersion || request.AgentID == "" || request.AgentID != options.AgentID ||
		!canonicalUUID(request.AgentID) || !canonicalUUID(request.CommandID) {
		return UpdateRequest{}, nil, errors.New("update request identity is invalid")
	}
	if request.Command.CommandID != request.CommandID || request.Command.AgentID != request.AgentID || request.Command.Action != controlproto.Upgrade ||
		request.Command.ControlVersion != options.ControlVersion || request.Command.Type != "command" || request.Command.ExpiresAt <= request.Command.IssuedAt ||
		controlproto.VerifyCommand(options.CommandPublicKey, request.Command) != nil {
		return UpdateRequest{}, nil, errors.New("update command envelope is invalid")
	}
	var commandPayload struct {
		Manifest     Manifest `json:"manifest"`
		Signature    string   `json:"signature"`
		PanelVersion string   `json:"panel_version"`
	}
	if err := controlproto.StrictDecodePayload(request.Command.Payload, &commandPayload); err != nil ||
		commandPayload.Manifest != request.Manifest || commandPayload.Signature != request.Signature || commandPayload.PanelVersion == "" {
		return UpdateRequest{}, nil, errors.New("update command payload does not match request")
	}
	signature, err := base64.StdEncoding.DecodeString(request.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || base64.StdEncoding.EncodeToString(signature) != request.Signature {
		return UpdateRequest{}, nil, ErrSignature
	}
	var artifact []byte
	if options.UpdatesDirFile != nil {
		artifact, err = readOwnedArtifactAt(options.UpdatesDirFile, request.ArtifactBasename, options.ExpectedUID, options.ExpectedGID)
	} else {
		artifact, err = readOwnedArtifact(filepath.Join(options.UpdatesDir, request.ArtifactBasename), options.ExpectedUID, options.ExpectedGID)
	}
	if err != nil {
		return UpdateRequest{}, nil, err
	}
	err = VerifyManifest(options.PublicKey, SignedManifest{Manifest: request.Manifest, Signature: signature}, VerifyOptions{
		CurrentVersion: options.CurrentVersion, PanelVersion: commandPayload.PanelVersion, OS: options.OS, Arch: options.Arch,
		ControlVersion: options.ControlVersion, Artifact: artifact, Now: options.Now,
	})
	if err != nil {
		return UpdateRequest{}, nil, err
	}
	return request, artifact, nil
}

func DecodeUpdateRequest(body []byte) (UpdateRequest, error) {
	if len(body) == 0 || len(body) > 128*1024 {
		return UpdateRequest{}, errors.New("update request size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var request UpdateRequest
	if err := decoder.Decode(&request); err != nil {
		return UpdateRequest{}, fmt.Errorf("decode update request: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return UpdateRequest{}, errors.New("update request contains trailing JSON")
	}
	return request, nil
}

func readOwnedArtifact(path string, expectedUID, expectedGID int) ([]byte, error) {
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	return readOwnedArtifactAt(directory, filepath.Base(path), expectedUID, expectedGID)
}

func readOwnedArtifactAt(directory *os.File, basename string, expectedUID, expectedGID int) ([]byte, error) {
	if directory == nil || !safeBasename(basename) {
		return nil, errors.New("update artifact basename is invalid")
	}
	fd, err := unix.Openat(int(directory.Fd()), basename, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errors.New("update artifact must be a regular file opened without following links")
	}
	file := os.NewFile(uintptr(fd), basename)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("open update artifact")
	}
	defer file.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nil, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return nil, errors.New("update artifact must be a regular file")
	}
	if stat.Mode&0o777 != 0o600 {
		return nil, errors.New("update artifact mode must be 0600")
	}
	if int(stat.Uid) != expectedUID || int(stat.Gid) != expectedGID {
		return nil, errors.New("update artifact owner is invalid")
	}
	body, err := io.ReadAll(io.LimitReader(file, MaxArtifactBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > MaxArtifactBytes {
		return nil, ErrArtifactSize
	}
	return body, nil
}

func safeBasename(value string) bool {
	return value != "" && value != "." && value != ".." && len(value) <= 255 && filepath.Base(value) == value &&
		!strings.ContainsAny(value, `/\`) && !strings.ContainsAny(value, "\r\n\x00")
}

func canonicalUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if value[index] != '-' {
				return false
			}
			continue
		}
		if !((value[index] >= '0' && value[index] <= '9') || (value[index] >= 'a' && value[index] <= 'f')) {
			return false
		}
	}
	return true
}
