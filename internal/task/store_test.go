package task

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"
)

func TestStoreCreateAndGetReturnCopies(t *testing.T) {
	store := NewStore()
	created, err := store.Create(Task{
		FileName:   "report.pdf",
		LocalPath:  `C:\files\report.pdf`,
		Direction:  DirectionSend,
		Peer:       "Laptop",
		TotalBytes: 4096,
		Status:     StatusPending,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ID == "" {
		t.Fatal("Create() returned an empty ID")
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("Create() timestamps = %v, %v; want non-zero", created.CreatedAt, created.UpdatedAt)
	}

	created.FileName = "changed-by-caller"
	got, ok := store.Get(created.ID)
	if !ok {
		t.Fatal("Get() did not find created task")
	}
	if got.FileName != "report.pdf" {
		t.Errorf("Get().FileName = %q, want report.pdf", got.FileName)
	}

	got.FileName = "changed-after-get"
	again, ok := store.Get(created.ID)
	if !ok {
		t.Fatal("second Get() did not find created task")
	}
	if again.FileName != "report.pdf" {
		t.Errorf("second Get().FileName = %q, want report.pdf", again.FileName)
	}
}

func TestStoreRejectsInvalidTaskValues(t *testing.T) {
	store := NewStore()
	for _, value := range []Task{
		{Status: "unknown"},
		{Status: StatusPending, Direction: "unknown"},
		{Status: StatusPending, Speed: -1},
		{Status: StatusPending, Speed: math.NaN()},
		{Status: StatusCompleted, TotalBytes: 10, TransferredBytes: 9},
	} {
		if _, err := store.Create(value); err == nil {
			t.Fatalf("Create(%+v) unexpectedly succeeded", value)
		}
	}
}

func TestStoreRejectsProgressRegressionAndTerminalMutation(t *testing.T) {
	store := NewStore()
	created, _ := store.Create(Task{ID: "progress", Status: StatusRunning, TotalBytes: 10, TransferredBytes: 5})
	if _, err := store.Update(created.ID, func(value *Task) error { value.TransferredBytes = 4; return nil }); !errors.Is(err, ErrInvalidProgress) {
		t.Fatalf("regression error = %v", err)
	}
	completed, _ := store.Create(Task{ID: "done", Status: StatusCompleted, TotalBytes: 10, TransferredBytes: 10})
	if _, err := store.Update(completed.ID, func(value *Task) error { value.Speed = 1; return nil }); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("terminal mutation error = %v", err)
	}
}

func TestStoreUpdateEnforcesIDsTransitionsAndProgress(t *testing.T) {
	store := NewStore()
	created, err := store.Create(Task{ID: "task-1", TotalBytes: 100, Status: StatusPending})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, err = store.Update(created.ID, func(value *Task) error {
		value.ID = "replacement"
		return nil
	})
	if !errors.Is(err, ErrImmutableID) {
		t.Fatalf("Update() ID error = %v, want ErrImmutableID", err)
	}

	for _, next := range []Status{StatusAccepted, StatusRunning} {
		updated, err := store.Update(created.ID, func(value *Task) error {
			value.Status = next
			return nil
		})
		if err != nil {
			t.Fatalf("Update() to %q error = %v", next, err)
		}
		if updated.Status != next {
			t.Fatalf("Update() status = %q, want %q", updated.Status, next)
		}
	}

	updated, err := store.Update(created.ID, func(value *Task) error {
		value.TransferredBytes = 60
		value.Speed = 2048
		return nil
	})
	if err != nil {
		t.Fatalf("progress Update() error = %v", err)
	}
	if updated.TransferredBytes != 60 || updated.Speed != 2048 {
		t.Fatalf("progress = %d bytes at %v, want 60 at 2048", updated.TransferredBytes, updated.Speed)
	}

	updated, err = store.Update(created.ID, func(value *Task) error {
		value.TransferredBytes = value.TotalBytes
		value.Status = StatusCompleted
		return nil
	})
	if err != nil {
		t.Fatalf("completion Update() error = %v", err)
	}
	if updated.Status != StatusCompleted {
		t.Fatalf("completion status = %q, want completed", updated.Status)
	}

	_, err = store.Update(created.ID, func(value *Task) error {
		value.Status = StatusRunning
		return nil
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("terminal Update() error = %v, want ErrInvalidTransition", err)
	}
}

func TestStoreRejectsInvalidUpdates(t *testing.T) {
	store := NewStore()
	if _, err := store.Update("missing", func(*Task) error { return nil }); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("Update() missing error = %v, want ErrTaskNotFound", err)
	}

	created, err := store.Create(Task{ID: "pending", TotalBytes: 10, Status: StatusPending})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	_, err = store.Update(created.ID, func(value *Task) error {
		value.Status = StatusCompleted
		return nil
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("pending-to-completed error = %v, want ErrInvalidTransition", err)
	}

	_, err = store.Update(created.ID, func(value *Task) error {
		value.TransferredBytes = value.TotalBytes + 1
		return nil
	})
	if !errors.Is(err, ErrInvalidProgress) {
		t.Fatalf("oversized progress error = %v, want ErrInvalidProgress", err)
	}
}

func TestStoreListIsSortedAndIsolated(t *testing.T) {
	store := NewStore()
	base := time.Date(2026, time.July, 18, 10, 0, 0, 0, time.UTC)
	for _, value := range []Task{
		{ID: "later", FileName: "later.txt", Status: StatusPending, CreatedAt: base.Add(time.Minute)},
		{ID: "earlier", FileName: "earlier.txt", Status: StatusPending, CreatedAt: base},
	} {
		if _, err := store.Create(value); err != nil {
			t.Fatalf("Create(%q) error = %v", value.ID, err)
		}
	}

	listed := store.List()
	if len(listed) != 2 {
		t.Fatalf("List() length = %d, want 2", len(listed))
	}
	if listed[0].ID != "earlier" || listed[1].ID != "later" {
		t.Fatalf("List() IDs = %q, %q; want earlier, later", listed[0].ID, listed[1].ID)
	}
	listed[0].FileName = "changed"
	again := store.List()
	if again[0].FileName != "earlier.txt" {
		t.Fatalf("second List().FileName = %q, want earlier.txt", again[0].FileName)
	}
}

func TestStoreSupportsConcurrentReadersAndWriters(t *testing.T) {
	store := NewStore()
	created, err := store.Create(Task{ID: "concurrent", TotalBytes: 1000, Status: StatusRunning})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var waitGroup sync.WaitGroup
	for range 10 {
		waitGroup.Add(2)
		go func() {
			defer waitGroup.Done()
			for range 50 {
				_, _ = store.Update(created.ID, func(value *Task) error {
					value.TransferredBytes++
					return nil
				})
			}
		}()
		go func() {
			defer waitGroup.Done()
			for range 50 {
				_, _ = store.Get(created.ID)
				_ = store.List()
			}
		}()
	}
	waitGroup.Wait()

	got, ok := store.Get(created.ID)
	if !ok {
		t.Fatal("Get() did not find concurrent task")
	}
	if got.TransferredBytes != 500 {
		t.Fatalf("TransferredBytes = %d, want 500", got.TransferredBytes)
	}
}
