package command

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/s2005lg/net-probe/internal/controlproto"
	"github.com/s2005lg/net-probe/internal/panel/auth"
	panelcontrol "github.com/s2005lg/net-probe/internal/panel/control"
)

type CommandSender interface {
	Send(agentID string, message []byte) error
}

type Dispatcher struct {
	store  *Store
	sender CommandSender
	now    func() time.Time
}

func NewDispatcher(store *Store, sender CommandSender) *Dispatcher {
	now := time.Now
	if store != nil && store.now != nil {
		now = store.now
	}
	return &Dispatcher{store: store, sender: sender, now: now}
}

func (d *Dispatcher) DispatchAgent(ctx context.Context, agentID string) error {
	if d == nil || d.store == nil || d.sender == nil {
		return errors.New("command dispatcher is not configured")
	}
	_, _ = d.store.Expire(ctx, d.now())
	commands, err := d.store.QueuedFor(ctx, agentID, d.now())
	if err != nil {
		return err
	}
	for _, command := range commands {
		actor, state, err := d.creatorAndState(ctx, command.CommandID)
		if err != nil {
			return err
		}
		required := auth.Operator
		if command.Action == controlproto.Upgrade {
			required = auth.Admin
		}
		if auth.Require(actor, required) != nil {
			if err := d.store.Transition(ctx, command.CommandID, Failed, actor, "authorization_revoked", "authorization_revoked", []byte(`{"status":"rejected"}`)); err != nil {
				return err
			}
			continue
		}
		body, err := json.Marshal(command)
		if err != nil {
			return err
		}
		if err := d.sender.Send(agentID, body); err != nil {
			if errors.Is(err, panelcontrol.ErrOffline) || errors.Is(err, panelcontrol.ErrBackpressure) {
				continue
			}
			return err
		}
		if state == Queued {
			if err := d.store.Transition(ctx, command.CommandID, Dispatched, actor, "hub_enqueued", "", nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *Dispatcher) creatorAndState(ctx context.Context, commandID string) (auth.Actor, State, error) {
	var actor auth.Actor
	var role string
	var state State
	err := d.store.db.QueryRowContext(ctx, `SELECT c.created_by_user_id,c.created_session_id,u.username,u.role,c.state
		FROM agent_commands c JOIN users u ON u.id=c.created_by_user_id WHERE c.command_id=?`, commandID).
		Scan(&actor.UserID, &actor.SessionID, &actor.Username, &role, &state)
	actor.Role = auth.Role(role)
	return actor, state, err
}
