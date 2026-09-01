package update

import (
	"context"
	"errors"
	"time"
)

type UpgradeProof struct {
	Version   string `json:"version"`
	AgentID   string `json:"agent_id"`
	CommandID string `json:"command_id"`
	BootID    string `json:"boot_id"`
}

type HelperResult struct {
	CommandID       string `json:"command_id"`
	State           string `json:"state"`
	Code            string `json:"code"`
	Version         string `json:"version"`
	PreviousVersion string `json:"previous_version"`
}

type HelperSystem interface {
	CurrentTarget(context.Context) (string, error)
	Install(context.Context, string, []byte) (string, error)
	Switch(context.Context, string) error
	Restart(context.Context) error
	WaitProof(context.Context, UpgradeProof) error
	Retain(context.Context, string, string) error
	WriteResult(context.Context, HelperResult) error
}

// ApplyVerifiedUpdate is the small privileged transaction. Callers must pass
// bytes already verified by ValidateUpdateRequest; the concrete root system
// independently performs that validation immediately before this call.
func ApplyVerifiedUpdate(ctx context.Context, system HelperSystem, request UpdateRequest, artifact []byte, proofTimeout time.Duration) (HelperResult, error) {
	result := HelperResult{
		CommandID: request.CommandID, State: "failed", Code: "upgrade_not_applied",
		Version: request.Manifest.Version, PreviousVersion: request.PreviousVersion,
	}
	writeResult := func(operationErr error) (HelperResult, error) {
		if writeErr := system.WriteResult(ctx, result); writeErr != nil {
			return result, errors.Join(operationErr, writeErr)
		}
		return result, operationErr
	}
	if system == nil || len(artifact) == 0 {
		return result, errors.New("verified update transaction is invalid")
	}
	previousTarget, err := system.CurrentTarget(ctx)
	if err != nil || previousTarget == "" {
		return writeResult(errors.New("read current Agent target"))
	}
	installedTarget, err := system.Install(ctx, request.Manifest.Version, artifact)
	if err != nil || installedTarget == "" || installedTarget == previousTarget {
		return writeResult(errors.New("install verified Agent artifact"))
	}
	if err := system.Switch(ctx, installedTarget); err != nil {
		return writeResult(errors.New("switch Agent target"))
	}
	rollback := func(cause error) (HelperResult, error) {
		rollbackErr := system.Switch(ctx, previousTarget)
		restartErr := system.Restart(ctx)
		result.Code = "upgrade_rolled_back"
		if rollbackErr != nil || restartErr != nil {
			result.Code = "upgrade_rollback_failed"
		}
		_, resultErr := writeResult(errors.Join(cause, rollbackErr, restartErr))
		return result, resultErr
	}
	if err := system.Restart(ctx); err != nil {
		return rollback(errors.New("restart upgraded Agent"))
	}
	if proofTimeout <= 0 || proofTimeout > 120*time.Second {
		proofTimeout = 120 * time.Second
	}
	proofContext, cancelProof := context.WithTimeout(ctx, proofTimeout)
	proof := UpgradeProof{Version: request.Manifest.Version, AgentID: request.AgentID, CommandID: request.CommandID}
	err = system.WaitProof(proofContext, proof)
	cancelProof()
	if err != nil {
		return rollback(errors.New("upgraded Agent did not prove readiness"))
	}
	if err := system.Retain(ctx, installedTarget, previousTarget); err != nil {
		return rollback(errors.New("retain Agent versions"))
	}
	result.State, result.Code = "succeeded", "upgrade_applied"
	return writeResult(nil)
}
