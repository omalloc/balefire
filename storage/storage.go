package storage

import (
	transportv1 "github.com/omalloc/balefire/api/transport/v1"
)

type TaskStore interface {
	// SaveTask saves a task to the store.
	SaveTask(task *transportv1.Message) error

	// GetTask retrieves a task by ID.
	GetTask(id string) (*transportv1.Message, error)

	// DeleteTask deletes a task by ID.
	DeleteTask(id string) error

	// ListPendingTasks returns a list of pending tasks.
	ListPendingTasks() ([]*transportv1.Message, error)

	// MarkTaskAsDone marks a task as completed.
	MarkTaskAsDone(id string) error

	// Close closes the store.
	Close() error
}
