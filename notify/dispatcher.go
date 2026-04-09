package notify

import (
	"context"
	"log/slog"

	"notification_relay/config"
	"notification_relay/db"
)

// Job represents a notification to be dispatched.
type Job struct {
	NotificationID string
}

// Dispatcher reads Jobs from the queue channel and fans them out to delivery providers.
// Implementation is a placeholder for the next development phase.
type Dispatcher struct {
	cfg    config.NotifyConfig
	q      *db.Queries
	queue  <-chan Job
	logger *slog.Logger
}

func NewDispatcher(cfg config.NotifyConfig, q *db.Queries, queue <-chan Job, logger *slog.Logger) *Dispatcher {
	return &Dispatcher{cfg: cfg, q: q, queue: queue, logger: logger}
}

// Run starts the dispatcher workers. It returns when ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) {
	d.logger.Info("dispatcher started", "workers", d.cfg.WorkerCount)
	for range d.cfg.WorkerCount {
		go d.worker(ctx)
	}
	<-ctx.Done()
	d.logger.Info("dispatcher shutting down")
}

func (d *Dispatcher) worker(ctx context.Context) {
	for {
		select {
		case job, ok := <-d.queue:
			if !ok {
				return
			}
			d.logger.Info("dispatch job received", "notification_id", job.NotificationID)
			// TODO: load notification, expand group members, create delivery rows, dispatch
		case <-ctx.Done():
			return
		}
	}
}
