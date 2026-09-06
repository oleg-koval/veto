package dispatch

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestAvailabilityExpiresAndPersistsAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "unavailable.json")
	store := NewAvailabilityStore(path)
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	if _, err := store.Set("claude", time.Hour); err != nil {
		t.Fatal(err)
	}
	if !store.IsUnavailable("claude") {
		t.Fatal("claude should be unavailable")
	}
	now = now.Add(2 * time.Hour)
	if store.IsUnavailable("claude") {
		t.Fatal("expired entry should not be active")
	}
	if err := store.Clear("claude"); err != nil {
		t.Fatal(err)
	}
}

func TestAvailabilityStoreSerializesConcurrentWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "unavailable.json")
	stores := []*AvailabilityStore{NewAvailabilityStore(path), NewAvailabilityStore(path)}
	var group sync.WaitGroup
	for index, store := range stores {
		group.Add(1)
		go func(index int, store *AvailabilityStore) {
			defer group.Done()
			agent := "claude"
			if index == 1 {
				agent = "codex"
			}
			if _, err := store.Set(agent, time.Hour); err != nil {
				t.Errorf("set %s: %v", agent, err)
			}
		}(index, store)
	}
	group.Wait()

	entries, err := NewAvailabilityStore(path).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("concurrent availability entries = %d, want 2: %#v", len(entries), entries)
	}
}
