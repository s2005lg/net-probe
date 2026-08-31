package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/s2005lg/net-probe/internal/sink"
)

var ErrReportPending = errors.New("one or more report destinations remain pending")

type Destination struct {
	ID   string
	Sink sink.Sink
}

type Reporter struct {
	outbox       *Outbox
	destinations map[string]sink.Sink
	orderedIDs   []string
}

func NewReporter(outbox *Outbox, destinations []Destination) (*Reporter, error) {
	if outbox == nil || len(destinations) == 0 {
		return nil, errors.New("reporter requires an outbox and destinations")
	}
	r := &Reporter{outbox: outbox, destinations: make(map[string]sink.Sink, len(destinations))}
	for _, destination := range destinations {
		if destination.ID == "" || destination.Sink == nil {
			return nil, errors.New("report destination ID and sink are required")
		}
		if _, exists := r.destinations[destination.ID]; exists {
			return nil, fmt.Errorf("duplicate report destination %q", destination.ID)
		}
		r.destinations[destination.ID] = destination.Sink
		r.orderedIDs = append(r.orderedIDs, destination.ID)
	}
	return r, nil
}

func (r *Reporter) Send(ctx context.Context, body []byte) error {
	if _, err := r.outbox.Put(body, r.orderedIDs); err != nil {
		return err
	}
	return r.Flush(ctx)
}

func (r *Reporter) Flush(ctx context.Context) error {
	items, err := r.outbox.List()
	if err != nil {
		return err
	}
	pending := false
	for _, item := range items {
		for _, destinationID := range item.Pending {
			if err := ctx.Err(); err != nil {
				return err
			}
			destination := r.destinations[destinationID]
			if destination == nil {
				pending = true
				continue
			}
			if err := destination.Send(ctx, item.Body); err != nil {
				pending = true
				continue
			}
			if err := r.outbox.Ack(item.ID, destinationID); err != nil {
				return err
			}
		}
	}
	if pending {
		return ErrReportPending
	}
	return nil
}

func (r *Reporter) OutboxDepth() int {
	if r == nil || r.outbox == nil {
		return 0
	}
	return r.outbox.Depth()
}
