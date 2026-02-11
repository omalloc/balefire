package storage

import (
	"errors"
	"fmt"

	"github.com/nutsdb/nutsdb"
	pb "github.com/omalloc/balefire/api/transport/v1"
	"google.golang.org/protobuf/proto"
)

const (
	bucketPending = "pending_tasks"
	bucketDone    = "done_tasks"
)

type nutsdbStore struct {
	db *nutsdb.DB
}

// NewNutsDBStore creates a new NutsDB-backed store.
func NewNutsDBStore(path string) (TaskStore, error) {
	opts := nutsdb.DefaultOptions
	opts.Dir = path
	db, err := nutsdb.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open nutsdb: %w", err)
	}

	// Ensure buckets exist
	if err := db.Update(func(tx *nutsdb.Tx) error {
		if err := tx.NewBucket(nutsdb.DataStructureBTree, bucketPending); err != nil {
			return err
		}
		if err := tx.NewBucket(nutsdb.DataStructureBTree, bucketDone); err != nil {
			return err
		}
		return nil
	}); err != nil {
		if !errors.Is(err, nutsdb.ErrBucketAlreadyExist) {
			_ = db.Close()
			return nil, fmt.Errorf("failed to create buckets: %w", err)
		}
	}

	return &nutsdbStore{db: db}, nil
}

// SaveTask saves a task to the store in the pending bucket.
func (s *nutsdbStore) SaveTask(task *pb.Message) error {
	data, err := proto.Marshal(task)
	if err != nil {
		return fmt.Errorf("failed to marshal task: %w", err)
	}

	return s.db.Update(func(tx *nutsdb.Tx) error {
		return tx.Put(bucketPending, []byte(task.Id), data, 0)
	})
}

// GetTask retrieves a task by ID. It checks pending first, then done.
func (s *nutsdbStore) GetTask(id string) (*pb.Message, error) {
	var data []byte
	err := s.db.View(func(tx *nutsdb.Tx) error {
		v, err := tx.Get(bucketPending, []byte(id))
		if err == nil {
			data = v
			return nil
		}
		v, err = tx.Get(bucketDone, []byte(id))
		if err == nil {
			data = v
			return nil
		}
		return err
	})

	if err != nil {
		return nil, err
	}

	var task pb.Message
	if err := proto.Unmarshal(data, &task); err != nil {
		return nil, fmt.Errorf("failed to unmarshal task: %w", err)
	}
	return &task, nil
}

// DeleteTask deletes a task from both pending and done buckets.
func (s *nutsdbStore) DeleteTask(id string) error {
	return s.db.Update(func(tx *nutsdb.Tx) error {
		_ = tx.Delete(bucketPending, []byte(id))
		_ = tx.Delete(bucketDone, []byte(id))
		return nil
	})
}

// ListPendingTasks returns a list of pending tasks.
func (s *nutsdbStore) ListPendingTasks() ([]*pb.Message, error) {
	var tasks []*pb.Message

	err := s.db.View(func(tx *nutsdb.Tx) error {
		entries, _, err := tx.GetAll(bucketPending)
		if err != nil {
			return err
		}

		for _, entry := range entries {
			var task pb.Message
			if err := proto.Unmarshal(entry, &task); err != nil {
				continue // skip malformed tasks
			}
			tasks = append(tasks, &task)
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	return tasks, nil
}

// MarkTaskAsDone moves a task from pending to done bucket.
func (s *nutsdbStore) MarkTaskAsDone(id string) error {
	return s.db.Update(func(tx *nutsdb.Tx) error {
		// Get from pending
		v, err := tx.Get(bucketPending, []byte(id))
		if err != nil {
			return err
		}

		// Save to done with TTL (e.g., 24 hours)
		// Note: NutsDB TTL is in seconds.
		if err := tx.Put(bucketDone, []byte(id), v, 24*3600); err != nil {
			return err
		}

		// Delete from pending
		return tx.Delete(bucketPending, []byte(id))
	})
}

// Close closes the store.
func (s *nutsdbStore) Close() error {
	return s.db.Close()
}
