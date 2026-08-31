package update

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	RootUpdatesDir  = "/var/lib/net-probe/updates"
	RootVersionsDir = "/opt/net-probe/versions"
	RootAgentLink   = "/usr/local/bin/net-probe"
	RootProofPath   = "/run/net-probe/upgrade-ready.json"
)

type RootHelperOptions struct {
	PublicKey      ed25519.PublicKey
	CurrentVersion string
	PanelVersion   string
	ControlVersion string
	AgentID        string
	AgentUID       int
	AgentGID       int
	ProofTimeout   time.Duration
	Now            time.Time
}

func RunHelper(ctx context.Context, options RootHelperOptions) error {
	if os.Geteuid() != 0 {
		return errors.New("update helper must run as root")
	}
	pendingPath := filepath.Join(RootUpdatesDir, PendingRequestFile)
	pendingBody, err := readFixedOwnedFile(pendingPath, options.AgentUID, options.AgentGID, 0o600, 128*1024)
	if err != nil {
		return fmt.Errorf("read pending update request: %w", err)
	}
	request, artifact, err := ValidateUpdateRequest(pendingBody, RequestValidationOptions{
		UpdatesDir: RootUpdatesDir, PublicKey: options.PublicKey, CurrentVersion: options.CurrentVersion,
		PanelVersion: options.PanelVersion, OS: runtime.GOOS, Arch: runtime.GOARCH, ControlVersion: options.ControlVersion,
		AgentID: options.AgentID, ExpectedUID: options.AgentUID, ExpectedGID: options.AgentGID, Now: options.Now,
	})
	if err != nil {
		return fmt.Errorf("validate pending update request: %w", err)
	}
	system := &rootHelperSystem{agentUID: options.AgentUID, agentGID: options.AgentGID}
	_, err = ApplyVerifiedUpdate(ctx, system, request, artifact, options.ProofTimeout)
	return err
}

type rootHelperSystem struct {
	agentUID int
	agentGID int
}

func (s *rootHelperSystem) CurrentTarget(context.Context) (string, error) {
	target, err := os.Readlink(RootAgentLink)
	if err != nil {
		return "", err
	}
	if !validVersionTarget(target) {
		return "", errors.New("current Agent symlink target is outside version store")
	}
	return target, nil
}

func (s *rootHelperSystem) Install(_ context.Context, version string, artifact []byte) (string, error) {
	versionDir := filepath.Join(RootVersionsDir, version)
	if filepath.Base(version) != version || strings.ContainsAny(version, `/\\\r\n\x00`) {
		return "", errors.New("invalid Agent version directory")
	}
	if err := os.MkdirAll(RootVersionsDir, 0o755); err != nil {
		return "", err
	}
	if err := os.Mkdir(versionDir, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	info, err := os.Lstat(versionDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("Agent version directory is invalid")
	}
	if stat, ok := info.Sys().(*unix.Stat_t); !ok || stat.Uid != 0 || stat.Gid != 0 || info.Mode().Perm() != 0o755 {
		return "", errors.New("Agent version directory ownership is invalid")
	}
	target := filepath.Join(versionDir, "net-probe")
	if err := writeAtomicOwned(target, artifact, 0o755, 0, 0); err != nil {
		return "", err
	}
	return target, nil
}

func (s *rootHelperSystem) Switch(_ context.Context, target string) error {
	if !validVersionTarget(target) {
		return errors.New("refuse invalid Agent switch target")
	}
	dir := filepath.Dir(RootAgentLink)
	temporary, err := os.CreateTemp(dir, ".net-probe-link-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Remove(temporaryPath); err != nil {
		return err
	}
	defer os.Remove(temporaryPath)
	if err := os.Symlink(target, temporaryPath); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, RootAgentLink); err != nil {
		return err
	}
	return syncRootDirectory(dir)
}

func (s *rootHelperSystem) Restart(ctx context.Context) error {
	_ = os.Remove(RootProofPath)
	command := exec.CommandContext(ctx, "systemctl", "restart", "net-probe.service")
	command.Stdin, command.Stdout, command.Stderr = nil, io.Discard, io.Discard
	return command.Run()
}

func (s *rootHelperSystem) WaitProof(ctx context.Context, expected UpgradeProof) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		body, err := readFixedOwnedFile(RootProofPath, s.agentUID, s.agentGID, 0o600, 16*1024)
		if err == nil {
			var proof UpgradeProof
			if decodeStrictJSON(body, &proof) == nil && proof.Version == expected.Version && proof.AgentID == expected.AgentID &&
				proof.CommandID == expected.CommandID && proof.BootID != "" && len(proof.BootID) <= 256 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *rootHelperSystem) Retain(_ context.Context, current, previous string) error {
	keep := map[string]struct{}{filepath.Base(filepath.Dir(current)): {}, filepath.Base(filepath.Dir(previous)): {}}
	entries, err := os.ReadDir(RootVersionsDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if _, retained := keep[entry.Name()]; retained {
			continue
		}
		if !entry.IsDir() || filepath.Base(entry.Name()) != entry.Name() || strings.ContainsAny(entry.Name(), `/\\\r\n\x00`) {
			return errors.New("unexpected entry in Agent version store")
		}
		path := filepath.Join(RootVersionsDir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe entry in Agent version store")
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return syncRootDirectory(RootVersionsDir)
}

func (s *rootHelperSystem) WriteResult(_ context.Context, result HelperResult) error {
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return writeAtomicOwned(filepath.Join(RootUpdatesDir, HelperResultFile), body, 0o600, s.agentUID, s.agentGID)
}

func validVersionTarget(target string) bool {
	clean := filepath.Clean(target)
	return filepath.IsAbs(clean) && strings.HasPrefix(clean, RootVersionsDir+string(os.PathSeparator)) &&
		filepath.Base(clean) == "net-probe" && filepath.Dir(filepath.Dir(clean)) == RootVersionsDir
}

func readFixedOwnedFile(path string, uid, gid int, mode os.FileMode, limit int64) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), filepath.Base(path))
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("open fixed file")
	}
	defer file.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nil, err
	}
	if uint32(stat.Mode)&unix.S_IFMT != unix.S_IFREG || uint32(stat.Mode)&0o777 != uint32(mode.Perm()) || int(stat.Uid) != uid || int(stat.Gid) != gid {
		return nil, errors.New("fixed file ownership or mode is invalid")
	}
	body, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(body)) > limit {
		return nil, errors.New("fixed file exceeds limit")
	}
	return body, nil
}

func writeAtomicOwned(target string, body []byte, mode os.FileMode, uid, gid int) error {
	temporary, err := os.CreateTemp(filepath.Dir(target), ".net-probe-update-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chown(uid, gid); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(body); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return err
	}
	return syncRootDirectory(filepath.Dir(target))
}

func syncRootDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func decodeStrictJSON(body []byte, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}
