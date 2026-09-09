package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/oleg-koval/veto/internal/controlplane"
	"github.com/oleg-koval/veto/pkg/ledger"
)

type tuiHistorySelector struct {
	RunID   string
	TaskID  string
	EventID string
}

func runTUIHistoryDelete(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
	scope := strings.TrimSpace(request.Arguments["scope"])
	home, err := os.UserHomeDir()
	if err != nil {
		return controlplane.ActionResult{ActionID: "history-delete"}, err
	}

	closeLogger()
	defer setupLogger()
	if scope == "all" {
		if err := removeAllTUIHistory(home); err != nil {
			return controlplane.ActionResult{ActionID: "history-delete"}, err
		}
		return controlplane.ActionResult{ActionID: "history-delete", Summary: "all mission history removed"}, nil
	}

	selector := tuiHistorySelector{
		RunID:   strings.TrimSpace(request.Arguments["run-id"]),
		TaskID:  strings.TrimSpace(request.Arguments["task-id"]),
		EventID: strings.TrimSpace(request.Arguments["event-id"]),
	}
	if selector.RunID == "" && selector.TaskID == "" && selector.EventID == "" {
		return controlplane.ActionResult{ActionID: "history-delete"}, fmt.Errorf("mission identity is required")
	}
	if err := removeSelectedTUIHistory(home, selector); err != nil {
		return controlplane.ActionResult{ActionID: "history-delete"}, err
	}
	return controlplane.ActionResult{ActionID: "history-delete", Summary: "selected mission removed"}, nil
}

func removeAllTUIHistory(home string) error {
	paths, err := filepath.Glob(filepath.Join(home, ".veto", "logs", "veto-*.log"))
	if err != nil {
		return fmt.Errorf("find mission logs: %w", err)
	}
	for _, path := range paths {
		lockFile, lockErr := lockMissionStoreFile(path + ".lock")
		if lockErr != nil {
			return fmt.Errorf("lock mission log %s: %w", filepath.Base(path), lockErr)
		}
		err := os.Remove(path)
		_ = lockFile.Close()
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove mission log %s: %w", filepath.Base(path), err)
		}
	}
	return replaceTUIHistoryIndex(home, []tuiMissionRecord{})
}

func removeSelectedTUIHistory(home string, selector tuiHistorySelector) error {
	paths, err := filepath.Glob(filepath.Join(home, ".veto", "logs", "veto-*.log"))
	if err != nil {
		return fmt.Errorf("find mission logs: %w", err)
	}
	runIDs := make(map[string]struct{})
	if selector.RunID != "" {
		runIDs[selector.RunID] = struct{}{}
	}
	for _, path := range paths {
		matched, err := removeMatchingTUILedgerLines(path, selector)
		if err != nil {
			return err
		}
		for runID := range matched {
			runIDs[runID] = struct{}{}
		}
	}
	if len(runIDs) > 0 {
		if err := removeMissionIndexRecords(home, runIDs); err != nil {
			return err
		}
	}
	return nil
}

func removeMatchingTUILedgerLines(path string, selector tuiHistorySelector) (map[string]struct{}, error) {
	matchedRunIDs := make(map[string]struct{})
	lockFile, lockErr := lockMissionStoreFile(path + ".lock")
	if lockErr != nil {
		return matchedRunIDs, fmt.Errorf("lock mission log %s: %w", filepath.Base(path), lockErr)
	}
	defer lockFile.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return matchedRunIDs, nil
		}
		return matchedRunIDs, fmt.Errorf("read mission log %s: %w", filepath.Base(path), err)
	}
	lines := bytes.Split(data, []byte{'\n'})
	var kept bytes.Buffer
	removed := false
	for index, line := range lines {
		remove := false
		var event ledger.Event
		if len(bytes.TrimSpace(line)) > 0 && json.Unmarshal(line, &event) == nil {
			remove = historyEventMatchesSelector(event, selector)
		}
		if remove {
			removed = true
			if event.RunID != "" {
				matchedRunIDs[event.RunID] = struct{}{}
			}
		} else {
			kept.Write(line)
			if index < len(lines)-1 {
				kept.WriteByte('\n')
			}
		}
	}
	if !removed {
		return matchedRunIDs, nil
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".veto-history-*.tmp")
	if err != nil {
		return matchedRunIDs, fmt.Errorf("create mission log temp file: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return matchedRunIDs, fmt.Errorf("protect mission log: %w", err)
	}
	if _, err := temporary.Write(kept.Bytes()); err != nil {
		_ = temporary.Close()
		return matchedRunIDs, fmt.Errorf("write mission log: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return matchedRunIDs, fmt.Errorf("close mission log: %w", err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return matchedRunIDs, fmt.Errorf("replace mission log: %w", err)
	}
	return matchedRunIDs, nil
}

func historyEventMatchesSelector(event ledger.Event, selector tuiHistorySelector) bool {
	if selector.RunID != "" {
		return event.RunID == selector.RunID
	}
	if selector.TaskID != "" {
		return event.TaskID == selector.TaskID
	}
	return selector.EventID != "" && event.EventID == selector.EventID
}

func removeMissionIndexRecord(home, runID string) error {
	return removeMissionIndexRecords(home, map[string]struct{}{runID: {}})
}

func removeMissionIndexRecords(home string, runIDs map[string]struct{}) error {
	path := filepath.Join(home, ".veto", "missions.json")
	missionStoreMu.Lock()
	defer missionStoreMu.Unlock()
	records, err := readTUIMissionRecords(path)
	if err != nil {
		return err
	}
	filtered := records[:0]
	for _, record := range records {
		if _, remove := runIDs[record.RunID]; !remove {
			filtered = append(filtered, record)
		}
	}
	return writeTUIMissionRecords(path, filtered)
}

func replaceTUIHistoryIndex(home string, records []tuiMissionRecord) error {
	path := filepath.Join(home, ".veto", "missions.json")
	missionStoreMu.Lock()
	defer missionStoreMu.Unlock()
	return writeTUIMissionRecords(path, records)
}
