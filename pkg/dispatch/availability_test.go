package dispatch

import (
	"path/filepath"
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
