package update

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	RootUpdatesDir  = "/var/lib/net-probe-updates"
	RootVersionsDir = "/opt/net-probe/versions"
	RootAgentLink   = "/usr/local/bin/net-probe"
	RootProofPath   = "/run/net-probe/upgrade-ready.json"
	RootTrustDir    = "/etc/net-probe/trust"
	RootCommandKey  = "/etc/net-probe/trust/command-signing.pub"
)

type RootHelperOptions struct {
	PublicKey        ed25519.PublicKey
	CommandPublicKey ed25519.PublicKey
	CurrentVersion   string
	ControlVersion   string
	AgentID          string
	AgentUID         int
	AgentGID         int
	ProofTimeout     time.Duration
	Now              time.Time
}

func RunHelper(ctx context.Context, options RootHelperOptions) error {
	if os.Geteuid() != 0 {
		return errors.New("update helper must run as root")
	}
	updates, err := openFixedDirectory(RootUpdatesDir, 0, options.AgentGID, 0o770)
	if err != nil {
		return fmt.Errorf("open root-owned update directory: %w", err)
	}
	defer updates.Close()
	commandKey, err := readRootCommandKey()
	if err != nil {
		return err
	}
	options.CommandPublicKey = commandKey
	bootID, err := readRootBootID()
	if err != nil {
		return err
	}
	system := &rootHelperSystem{agentUID: options.AgentUID, agentGID: options.AgentGID, updates: updates, bootID: bootID}
	return processPendingUpdate(ctx, options, updates, system)
}

func processPendingUpdate(ctx context.Context, options RootHelperOptions, updates *os.File, system HelperSystem) error {
	claimed, err := fileExistsAt(updates, ClaimedRequestFile)
	if err != nil {
		return err
	}
	if !claimed {
		if err := unix.Renameat(int(updates.Fd()), PendingRequestFile, int(updates.Fd()), ClaimedRequestFile); err != nil {
			if errors.Is(err, unix.ENOENT) {
				return nil
			}
			return fmt.Errorf("claim pending update request: %w", err)
		}
		if err := updates.Sync(); err != nil {
			return err
		}
	}
	pendingBody, err := readFixedOwnedFileAt(updates, ClaimedRequestFile, options.AgentUID, options.AgentGID, 0o600, 128*1024)
	if err != nil {
		return quarantineUncorrelatedUpdate(updates, fmt.Errorf("read claimed update request: %w", err))
	}
	decoded, decodeErr := DecodeUpdateRequest(pendingBody)
	if decodeErr != nil {
		return quarantineUncorrelatedUpdate(updates, decodeErr)
	}
	request, artifact, err := ValidateUpdateRequest(pendingBody, RequestValidationOptions{
		UpdatesDirFile: updates, PublicKey: options.PublicKey, CurrentVersion: options.CurrentVersion,
		CommandPublicKey: options.CommandPublicKey,
		OS:               runtime.GOOS, Arch: runtime.GOARCH, ControlVersion: options.ControlVersion,
		AgentID: options.AgentID, ExpectedUID: options.AgentUID, ExpectedGID: options.AgentGID, Now: options.Now,
	})
	if err != nil {
		return rejectPendingUpdate(ctx, updates, system, decoded, fmt.Errorf("validate pending update request: %w", err))
	}
	_, err = ApplyVerifiedUpdate(ctx, system, request, artifact, options.ProofTimeout)
	return err
}

func readRootCommandKey() (ed25519.PublicKey, error) {
	directory, err := openFixedDirectory(RootTrustDir, 0, 0, 0o755)
	if err != nil {
		return nil, fmt.Errorf("open root trust directory: %w", err)
	}
	defer directory.Close()
	body, err := readFixedOwnedFileAt(directory, filepath.Base(RootCommandKey), 0, 0, 0o644, 16*1024)
	if err != nil {
		return nil, fmt.Errorf("read root command key: %w", err)
	}
	block, rest := pem.Decode(body)
	if block == nil || block.Type != "PUBLIC KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("root command key PEM is invalid")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	public, ok := parsed.(ed25519.PublicKey)
	if err != nil || !ok || len(public) != ed25519.PublicKeySize {
		return nil, errors.New("root command key is not Ed25519")
	}
	return ed25519.PublicKey(bytes.Clone(public)), nil
}

func rejectPendingUpdate(ctx context.Context, updates *os.File, system HelperSystem, request UpdateRequest, cause error) error {
	if !canonicalUUID(request.CommandID) {
		return quarantineUncorrelatedUpdate(updates, cause)
	}
	if err := unix.Renameat(int(updates.Fd()), ClaimedRequestFile, int(updates.Fd()), RejectedRequestFile); err != nil {
		return errors.Join(cause, fmt.Errorf("quarantine rejected update: %w", err))
	}
	if err := updates.Sync(); err != nil {
		return errors.Join(cause, err)
	}
	result := HelperResult{
		State: "failed", Code: "upgrade_rejected", CommandID: request.CommandID,
		Version: request.Manifest.Version, PreviousVersion: request.PreviousVersion,
	}
	if err := system.WriteResult(ctx, result); err != nil {
		return errors.Join(cause, fmt.Errorf("write rejected update result: %w", err))
	}
	return cause
}

func quarantineUncorrelatedUpdate(updates *os.File, cause error) error {
	if err := unix.Renameat(int(updates.Fd()), ClaimedRequestFile, int(updates.Fd()), RejectedRequestFile); err != nil {
		return errors.Join(cause, fmt.Errorf("quarantine uncorrelated update: %w", err))
	}
	if err := updates.Sync(); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func fileExistsAt(directory *os.File, basename string) (bool, error) {
	var stat unix.Stat_t
	err := unix.Fstatat(int(directory.Fd()), basename, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	return err == nil, err
}

type rootHelperSystem struct {
	agentUID int
	agentGID int
	updates  *os.File
	bootID   string
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
			if decodeStrictJSON(body, &proof) == nil && validUpgradeProof(proof, expected, s.bootID) {
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

func validUpgradeProof(proof, expected UpgradeProof, bootID string) bool {
	return bootID != "" && proof.Version == expected.Version && proof.AgentID == expected.AgentID &&
		proof.CommandID == expected.CommandID && proof.BootID == bootID
}

func readRootBootID() (string, error) {
	body, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	value := strings.TrimSpace(string(body))
	if err != nil || value == "" || len(value) > 256 {
		return "", errors.New("read kernel boot ID")
	}
	return value, nil
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
	return writeAtomicOwnedAt(s.updates, HelperResultFile, body, 0o640, 0, s.agentGID)
}

func validVersionTarget(target string) bool {
	clean := filepath.Clean(target)
	return filepath.IsAbs(clean) && strings.HasPrefix(clean, RootVersionsDir+string(os.PathSeparator)) &&
		filepath.Base(clean) == "net-probe" && filepath.Dir(filepath.Dir(clean)) == RootVersionsDir
}

func readFixedOwnedFile(path string, uid, gid int, mode os.FileMode, limit int64) ([]byte, error) {
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	return readFixedOwnedFileAt(directory, filepath.Base(path), uid, gid, mode, limit)
}

func readFixedOwnedFileAt(directory *os.File, basename string, uid, gid int, mode os.FileMode, limit int64) ([]byte, error) {
	if directory == nil || !safeBasename(basename) {
		return nil, errors.New("fixed file basename is invalid")
	}
	fd, err := unix.Openat(int(directory.Fd()), basename, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), basename)
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

func openFixedDirectory(path string, uid, gid int, mode os.FileMode) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), filepath.Base(path))
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("open fixed directory")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		file.Close()
		return nil, err
	}
	if uint32(stat.Mode)&unix.S_IFMT != unix.S_IFDIR || uint32(stat.Mode)&0o777 != uint32(mode.Perm()) || int(stat.Uid) != uid || int(stat.Gid) != gid {
		file.Close()
		return nil, errors.New("fixed directory ownership or mode is invalid")
	}
	return file, nil
}

func writeAtomicOwnedAt(directory *os.File, target string, body []byte, mode os.FileMode, uid, gid int) error {
	if directory == nil || !safeBasename(target) {
		return errors.New("atomic target basename is invalid")
	}
	var temporary string
	var fd int
	var err error
	for attempt := 0; attempt < 16; attempt++ {
		temporary = fmt.Sprintf(".net-probe-update-%d-%016x", os.Getpid(), rand.Uint64())
		fd, err = unix.Openat(int(directory.Fd()), temporary, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, uint32(mode.Perm()))
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EEXIST) {
			return err
		}
	}
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), temporary)
	if file == nil {
		_ = unix.Close(fd)
		return errors.New("create atomic update file")
	}
	defer unix.Unlinkat(int(directory.Fd()), temporary, 0)
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	if err := file.Chown(uid, gid); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(body); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := unix.Renameat(int(directory.Fd()), temporary, int(directory.Fd()), target); err != nil {
		return err
	}
	return directory.Sync()
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
