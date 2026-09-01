package update

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

type fakeHelperSystem struct {
	current       string
	installed     string
	installErr    error
	switchErrAt   int
	restartErrAt  int
	proofErr      error
	switches      []string
	restarts      int
	retained      [][2]string
	writtenResult []HelperResult
}

func (f *fakeHelperSystem) CurrentTarget(context.Context) (string, error) { return f.current, nil }
func (f *fakeHelperSystem) Install(context.Context, string, []byte) (string, error) {
	if f.installErr != nil {
		return "", f.installErr
	}
	return f.installed, nil
}
func (f *fakeHelperSystem) Switch(_ context.Context, target string) error {
	f.switches = append(f.switches, target)
	if f.switchErrAt > 0 && len(f.switches) == f.switchErrAt {
		return errors.New("switch failed")
	}
	f.current = target
	return nil
}
func (f *fakeHelperSystem) Restart(context.Context) error {
	f.restarts++
	if f.restartErrAt > 0 && f.restarts == f.restartErrAt {
		return errors.New("restart failed")
	}
	return nil
}
func (f *fakeHelperSystem) WaitProof(context.Context, UpgradeProof) error { return f.proofErr }
func (f *fakeHelperSystem) Retain(_ context.Context, current, previous string) error {
	f.retained = append(f.retained, [2]string{current, previous})
	return nil
}
func (f *fakeHelperSystem) WriteResult(_ context.Context, result HelperResult) error {
	f.writtenResult = append(f.writtenResult, result)
	return nil
}

func verifiedHelperFixture() (UpdateRequest, []byte, *fakeHelperSystem) {
	request := UpdateRequest{
		Manifest: Manifest{Version: "v1.2.4"}, PreviousVersion: "v1.2.3",
		AgentID: "123e4567-e89b-42d3-a456-426614174040", CommandID: "123e4567-e89b-42d3-a456-426614174041",
	}
	system := &fakeHelperSystem{
		current: "/opt/net-probe/versions/v1.2.3/net-probe", installed: "/opt/net-probe/versions/v1.2.4/net-probe",
	}
	return request, []byte("verified artifact"), system
}

func TestApplyVerifiedUpdateSwitchesRestartsProvesAndRetainsTwo(t *testing.T) {
	request, artifact, system := verifiedHelperFixture()
	result, err := ApplyVerifiedUpdate(context.Background(), system, request, artifact, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "succeeded" || result.Code != "upgrade_applied" || system.restarts != 1 ||
		len(system.switches) != 1 || system.switches[0] != system.installed || len(system.retained) != 1 ||
		system.retained[0] != [2]string{system.installed, "/opt/net-probe/versions/v1.2.3/net-probe"} {
		t.Fatalf("result=%+v system=%+v", result, system)
	}
	if len(system.writtenResult) != 1 || system.writtenResult[0] != result {
		t.Fatalf("written=%+v", system.writtenResult)
	}
}

func TestApplyVerifiedUpdateRollsBackOnProofOrRestartFailure(t *testing.T) {
	for name, configure := range map[string]func(*fakeHelperSystem){
		"proof":   func(system *fakeHelperSystem) { system.proofErr = errors.New("no proof") },
		"restart": func(system *fakeHelperSystem) { system.restartErrAt = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			request, artifact, system := verifiedHelperFixture()
			configure(system)
			result, err := ApplyVerifiedUpdate(context.Background(), system, request, artifact, 20*time.Millisecond)
			if err == nil || result.State != "failed" || result.Code != "upgrade_rolled_back" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if len(system.switches) != 2 || system.switches[1] != "/opt/net-probe/versions/v1.2.3/net-probe" || system.restarts < 2 {
				t.Fatalf("rollback system=%+v", system)
			}
			if len(system.writtenResult) != 1 || system.writtenResult[0].Code != "upgrade_rolled_back" {
				t.Fatalf("written=%+v", system.writtenResult)
			}
		})
	}
}

func TestApplyVerifiedUpdateDoesNotSwitchOnInstallOrInitialSwitchFailure(t *testing.T) {
	for name, configure := range map[string]func(*fakeHelperSystem){
		"disk full": func(system *fakeHelperSystem) { system.installErr = errors.New("disk full") },
		"switch":    func(system *fakeHelperSystem) { system.switchErrAt = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			request, artifact, system := verifiedHelperFixture()
			configure(system)
			result, err := ApplyVerifiedUpdate(context.Background(), system, request, artifact, 20*time.Millisecond)
			if err == nil || result.State != "failed" || result.Code != "upgrade_not_applied" || system.restarts != 0 {
				t.Fatalf("result=%+v err=%v system=%+v", result, err, system)
			}
			if system.current != "/opt/net-probe/versions/v1.2.3/net-probe" {
				t.Fatalf("current=%q", system.current)
			}
		})
	}
}

func TestProcessPendingUpdateQuarantinesValidationFailureAndWritesTerminalResult(t *testing.T) {
	dir := t.TempDir()
	request, validation := validUpdateRequest(t, dir)
	artifactPath := filepath.Join(dir, request.ArtifactBasename)
	if err := os.WriteFile(artifactPath, []byte("changed artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(request)
	if err := os.WriteFile(filepath.Join(dir, PendingRequestFile), body, 0o600); err != nil {
		t.Fatal(err)
	}
	directory, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	system := &fakeHelperSystem{}
	err = processPendingUpdate(context.Background(), RootHelperOptions{
		PublicKey: validation.PublicKey, CurrentVersion: validation.CurrentVersion,
		CommandPublicKey: validation.CommandPublicKey,
		ControlVersion:   validation.ControlVersion, AgentID: validation.AgentID,
		AgentUID: validation.ExpectedUID, AgentGID: validation.ExpectedGID, Now: validation.Now,
	}, directory, system)
	if err == nil {
		t.Fatal("invalid artifact was accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, PendingRequestFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending request still triggers path unit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, RejectedRequestFile)); err != nil {
		t.Fatalf("rejected request was not quarantined: %v", err)
	}
	if len(system.writtenResult) != 1 || system.writtenResult[0].State != "failed" ||
		system.writtenResult[0].Code != "upgrade_rejected" || system.writtenResult[0].CommandID != request.CommandID {
		t.Fatalf("written result=%+v runtime=%s", system.writtenResult, runtime.GOOS)
	}
}

func TestUpgradeProofMustMatchKernelBootID(t *testing.T) {
	expected := UpgradeProof{Version: "v1.2.4", AgentID: "agent", CommandID: "command"}
	proof := expected
	proof.BootID = "boot-current"
	if !validUpgradeProof(proof, expected, "boot-current") {
		t.Fatal("matching proof was rejected")
	}
	if validUpgradeProof(proof, expected, "boot-previous") || validUpgradeProof(proof, expected, "") {
		t.Fatal("stale or empty boot ID was accepted")
	}
}

func TestProcessPendingUpdateIgnoresMissingAndDoesNotCorrelateMalformedRequest(t *testing.T) {
	for name, createMalformed := range map[string]bool{"missing": false, "malformed": true} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if createMalformed {
				if err := os.WriteFile(filepath.Join(dir, PendingRequestFile), []byte(`{"command_id":`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			directory, err := os.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer directory.Close()
			system := &fakeHelperSystem{}
			err = processPendingUpdate(context.Background(), RootHelperOptions{AgentUID: os.Getuid(), AgentGID: os.Getgid()}, directory, system)
			if !createMalformed && err != nil {
				t.Fatalf("missing pending request err=%v", err)
			}
			if createMalformed && err == nil {
				t.Fatal("malformed request was accepted")
			}
			if len(system.writtenResult) != 0 {
				t.Fatalf("uncorrelated result was written: %+v", system.writtenResult)
			}
		})
	}
}
