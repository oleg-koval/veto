//go:build windows

package dispatch

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestAvailabilityStoreConcurrentReadsNeverObserveMissingReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unavailable.json")
	writer := NewAvailabilityStore(path)
	if _, err := writer.Set("claude", time.Hour); err != nil {
		t.Fatal(err)
	}

	var group sync.WaitGroup
	group.Add(1)
	defer group.Wait()
	go func() {
		defer group.Done()
		for index := 0; index < 100; index++ {
			agent := "claude"
			if index%2 == 1 {
				agent = "codex"
			}
			if _, err := writer.Set(agent, time.Hour); err != nil {
				t.Errorf("set: %v", err)
				return
			}
		}
	}()
	reader := NewAvailabilityStore(path)
	for index := 0; index < 100; index++ {
		if _, err := reader.List(); err != nil {
			t.Fatalf("concurrent list: %v", err)
		}
	}
}
