package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/oleg-koval/veto/pkg/ledger"
)

const (
	missionStoreVersion = 1
	maxMissionRecords   = 500
	maxMissionPrompt    = 64 * 1024
)

var missionStoreMu sync.Mutex

type tuiMissionRecord struct {
	Version   int       `json:"version"`
	RunID     string    `json:"run_id"`
	TaskID    string    `json:"task_id,omitempty"`
	Kind      string    `json:"kind,omitempty"`
	Risk      string    `json:"risk,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Title     string    `json:"title"`
	Objective string    `json:"objective"`
}

func tuiMissionStorePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".veto", "missions.json"), nil
}

// saveTUIMission persists one mission descriptor. New TUI submissions keep
// objectives out of the index by default; explicit callers may provide one,
// which is redacted and bounded before persistence.
func saveTUIMission(record tuiMissionRecord) error {
	if strings.TrimSpace(record.RunID) == "" {
		return fmt.Errorf("mission record requires run_id")
	}
	record.Version = missionStoreVersion
	record.CreatedAt = record.CreatedAt.UTC()
	if strings.TrimSpace(record.Objective) != "" {
		record.Objective = boundMissionPrompt(ledger.RedactUnbounded(record.Objective))
		record.Title = missionTitle(record.Objective)
	} else {
		record.Objective = ""
		record.Title = ""
	}

	path, err := tuiMissionStorePath()
	if err != nil {
		return err
	}
	missionStoreMu.Lock()
	defer missionStoreMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create mission index directory: %w", err)
	}
	lockFile, err := lockMissionStoreFile(path + ".lock")
	if err != nil {
		return fmt.Errorf("lock mission index: %w", err)
	}
	defer lockFile.Close()

	records := make([]tuiMissionRecord, 0, maxMissionRecords)
	data, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return fmt.Errorf("read mission index: %w", readErr)
	}
	if readErr == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &records); err != nil {
			return fmt.Errorf("read mission index: %w", err)
		}
	}
	updated := false
	for index := range records {
		if records[index].RunID == record.RunID {
			records[index] = record
			updated = true
			break
		}
	}
	if !updated {
		records = append(records, record)
	}
	sort.SliceStable(records, func(left, right int) bool { return records[left].CreatedAt.Before(records[right].CreatedAt) })
	if len(records) > maxMissionRecords {
		records = records[len(records)-maxMissionRecords:]
	}
	return writeTUIMissionRecords(path, records)
}

func writeTUIMissionRecords(path string, records []tuiMissionRecord) error {
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return fmt.Errorf("encode mission index: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".missions-*.tmp")
	if err != nil {
		return fmt.Errorf("create mission index temp file: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect mission index: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write mission index: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close mission index: %w", err)
	}
	if err := replaceMissionIndex(temporaryName, path); err != nil {
		return fmt.Errorf("replace mission index: %w", err)
	}
	return nil
}

func readTUIMissionRecords(path string) ([]tuiMissionRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	var records []tuiMissionRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("read mission index: %w", err)
	}
	return records, nil
}

func readTUIMissions() map[string]tuiMissionRecord {
	path, err := tuiMissionStorePath()
	if err != nil {
		return nil
	}
	records, err := readTUIMissionRecords(path)
	if err != nil {
		return nil
	}
	result := make(map[string]tuiMissionRecord, len(records))
	for _, record := range records {
		if record.RunID != "" {
			result[record.RunID] = record
		}
	}
	return result
}

func boundMissionPrompt(value string) string {
	if len(value) <= maxMissionPrompt {
		return value
	}
	limit := maxMissionPrompt
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit] + "\n[initial prompt truncated at 64 KiB]"
}

func missionTitle(objective string) string {
	line := strings.TrimSpace(strings.SplitN(objective, "\n", 2)[0])
	line = strings.Join(strings.Fields(line), " ")
	if len(line) > 96 {
		limit := 93
		for limit > 0 && !utf8.RuneStart(line[limit]) {
			limit--
		}
		return line[:limit] + "..."
	}
	return line
}
