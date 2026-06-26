package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Checkpoint persists the routing state across interruptions so the user can resume.
// Stored at ~/.veto/checkpoints/<hash>.json.
type Checkpoint struct {
	Hash      string
	Objective string
	Tried     []triedEntry
	SavedAt   time.Time
}

type triedEntry struct {
	Model    string
	Accepted bool
	Reasons  []string
}

func (cp *Checkpoint) triedNames() []string {
	names := make([]string, len(cp.Tried))
	for i, e := range cp.Tried {
		names[i] = e.Model
	}
	return names
}

func (cp *Checkpoint) add(model string, accepted bool, reasons []string) {
	cp.Tried = append(cp.Tried, triedEntry{Model: model, Accepted: accepted, Reasons: reasons})
	cp.SavedAt = time.Now()
}

func taskHash(objective, kind, risk string, maxCost float64) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%.6f", objective, kind, risk, maxCost)))
	return fmt.Sprintf("%x", h[:8]) // 16 hex chars — short, collision-resistant enough for a temp file
}

func checkpointPath(hash string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".veto", "checkpoints", hash+".json")
}

func loadCheckpoint(hash string) (*Checkpoint, bool) {
	data, err := os.ReadFile(checkpointPath(hash))
	if err != nil {
		return nil, false
	}
	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, false
	}
	return &cp, true
}

func saveCheckpoint(cp *Checkpoint) {
	path := checkpointPath(cp.Hash)
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	data, _ := json.MarshalIndent(cp, "", "  ")
	_ = os.WriteFile(path, data, 0600)
}

func deleteCheckpoint(hash string) {
	_ = os.Remove(checkpointPath(hash))
}
