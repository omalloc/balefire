package transport

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/transport"
	api "github.com/omalloc/balefire/api/transport"
	pb "github.com/omalloc/balefire/api/transport/v1"
	"github.com/omalloc/balefire/storage"
)

type OutboxService interface {
	// Start and Stop are for transport.Server.
	// inject kratos lifecycle.
	transport.Server

	// Send sends a message reliably.
	Send(ctx context.Context, dst string, message *pb.Message) error
}

type simpleOutbox struct {
	transport api.Transport
	store     storage.TaskStore
	stopCh    chan struct{}
	wg        sync.WaitGroup
	retryInt  time.Duration
}

// NewOutboxService creates a new Outbox.
func NewOutboxService(transport api.Transport, store storage.TaskStore) OutboxService {
	return &simpleOutbox{
		transport: transport,
		store:     store,
		stopCh:    make(chan struct{}),
		retryInt:  5 * time.Second, // Default retry interval
	}
}

// Send implements Outbox.Send.
func (o *simpleOutbox) Send(ctx context.Context, dst string, message *pb.Message) error {
	// 0. Ensure metadata exists and store destination
	if message.Metadata == nil {
		message.Metadata = make(map[string]string)
	}
	message.Metadata["destination"] = dst

	// 1. Save to store (PENDING)
	if err := o.store.SaveTask(message); err != nil {
		return fmt.Errorf("failed to save task: %w", err)
	}

	// 2. Try to send immediately
	if err := o.transport.Send(ctx, dst, message); err != nil {
		log.Warnf("failed to send task %s immediately: %v", message.Id, err)
		return nil // Return nil because it's saved and will be retried
	}

	// 3. Mark as DONE
	if err := o.store.MarkTaskAsDone(message.Id); err != nil {
		log.Errorf("failed to mark task %s as done: %v", message.Id, err)
	}

	return nil
}

// Start implements Outbox.Start.
func (o *simpleOutbox) Start(ctx context.Context) error {
	o.wg.Add(1)
	go o.retryLoop(ctx)
	return nil
}

// Stop implements Outbox.Stop.
func (o *simpleOutbox) Stop(_ context.Context) error {
	close(o.stopCh)
	o.wg.Wait()
	return nil
}

func (o *simpleOutbox) retryLoop(ctx context.Context) {
	defer o.wg.Done()
	ticker := time.NewTicker(o.retryInt)
	defer ticker.Stop()

	for {
		select {
		case <-o.stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			o.retryPendingTasks(ctx)
		}
	}
}

func (o *simpleOutbox) retryPendingTasks(ctx context.Context) {
	tasks, err := o.store.ListPendingTasks()
	if err != nil {
		log.Errorf("failed to list pending tasks: %v", err)
		return
	}

	for _, task := range tasks {
		// Check for exponential backoff
		if nextRetryStr, ok := task.Metadata["next_retry"]; ok {
			var nextRetry int64
			_, _ = fmt.Sscanf(nextRetryStr, "%d", &nextRetry)
			if time.Now().UnixNano() < nextRetry {
				continue
			}
		}

		dst := task.Metadata["destination"]
		if dst == "" {
			log.Errorf("task %s has no destination in metadata, dropping", task.Id)
			_ = o.store.DeleteTask(task.Id)
			continue
		}

		if err := o.transport.Send(ctx, dst, task); err == nil {
			log.Infof("successfully retried task %s", task.Id)
			if err := o.store.MarkTaskAsDone(task.Id); err != nil {
				log.Errorf("failed to mark retried task %s as done: %v", task.Id, err)
			}
		} else {
			log.Warnf("failed to retry task %s: %v", task.Id, err)

			// Update retry metadata
			retryCount := 0
			if countStr, ok := task.Metadata["retry_count"]; ok {
				_, _ = fmt.Sscanf(countStr, "%d", &retryCount)
			}
			retryCount++
			task.Metadata["retry_count"] = fmt.Sprintf("%d", retryCount)

			// Calculate next retry time: base * 2^retries
			backoff := time.Second * time.Duration(1<<retryCount)
			if backoff > time.Hour {
				backoff = time.Hour
			}
			task.Metadata["next_retry"] = fmt.Sprintf("%d", time.Now().Add(backoff).UnixNano())

			if err := o.store.SaveTask(task); err != nil {
				log.Errorf("failed to update task retry metadata: %v", err)
			}
		}
	}
}
