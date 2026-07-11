package handlers

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/cozyGarage/transformer"
	"github.com/cozyGarage/transformer/broker"
	"github.com/cozyGarage/transformer/datastore"
	"github.com/cozyGarage/transformer/middleware/task"
)

const maxRedeliveries = 5

type redeliveredHandler struct {
	ds     datastore.Datastore
	broker broker.Broker
}

func NewRedeliveredHandler(ds datastore.Datastore, b broker.Broker) task.HandlerFunc {
	h := &redeliveredHandler{
		ds:     ds,
		broker: b,
	}
	return h.handle
}

func (h *redeliveredHandler) handle(ctx context.Context, et task.EventType, t *tork.Task) error {
	log.Debug().
		Str("task-id", t.ID).
		Msg("received task redelivery")
	if t.Redelivered >= maxRedeliveries {
		log.Error().
			Str("task-id", t.ID).
			Msg("task redelivered too many times")
		now := time.Now().UTC()
		t.Error = "task redelivered too many times"
		t.FailedAt = &now
		t.State = tork.TaskStateFailed
		return h.broker.PublishTask(ctx, broker.QUEUE_ERROR, t)
	}
	t.Redelivered++
	return h.broker.PublishTask(ctx, t.Queue, t)
}
