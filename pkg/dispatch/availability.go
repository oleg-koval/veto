package dispatch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const availabilitySchemaVersion = 1

type Unavailability struct {
	Agent    string    `json:"agent"`
	Expires  time.Time `json:"expires"`
	MarkedAt time.Time `json:"marked_at"`
}

type availabilityFile struct {
	Version int              `json:"version"`
	Entries []Unavailability `json:"entries"`
}

type AvailabilityStore struct {
	mu   sync.Mutex
	path string
	now  func() time.Time
}

func NewAvailabilityStore(path string) *AvailabilityStore {
	return &AvailabilityStore{path: path, now: time.Now}
}

func (s *AvailabilityStore) List() ([]Unavailability, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.read()
	if err != nil {
		if os.IsNotExist(err) {
			return []Unavailability{}, nil
		}
		return nil, err
	}
	return active(entries, s.now()), nil
}

func (s *AvailabilityStore) Set(agent string, duration time.Duration) (Unavailability, error) {
	if duration <= 0 {
		return Unavailability{}, fmt.Errorf("duration must be positive")
	}
	agent = strings.ToLower(strings.TrimSpace(agent))
	if agent != "claude" && agent != "codex" {
		return Unavailability{}, fmt.Errorf("unsupported native agent %q", agent)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := acquireAvailabilityLock(s.path)
	if err != nil {
		return Unavailability{}, err
	}
	defer func() { _ = unlock() }()
	now := s.now().UTC()
	entries, err := s.read()
	if err != nil && !os.IsNotExist(err) {
		return Unavailability{}, err
	}
	entry := Unavailability{Agent: agent, MarkedAt: now, Expires: now.Add(duration)}
	filtered := make([]Unavailability, 0, 2)
	for _, current := range active(entries, now) {
		if current.Agent != agent {
			filtered = append(filtered, current)
		}
	}
	filtered = append(filtered, entry)
	if err := s.write(filtered); err != nil {
		return Unavailability{}, err
	}
	return entry, nil
}

func (s *AvailabilityStore) Clear(agent string) error {
	agent = strings.ToLower(strings.TrimSpace(agent))
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := acquireAvailabilityLock(s.path)
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()
	entries, err := s.read()
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	filtered := make([]Unavailability, 0, len(entries))
	for _, current := range active(entries, s.now()) {
		if agent == "" || current.Agent == agent {
			continue
		}
		filtered = append(filtered, current)
	}
	if len(filtered) == 0 {
		if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return s.write(filtered)
}

func (s *AvailabilityStore) IsUnavailable(agent string) bool {
	entries, err := s.List()
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if strings.EqualFold(entry.Agent, agent) {
			return true
		}
	}
	return false
}

func (s *AvailabilityStore) read() ([]Unavailability, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	var file availabilityFile
	if err := json.Unmarshal(data, &file); err != nil || file.Version != availabilitySchemaVersion {
		return nil, fmt.Errorf("invalid availability state")
	}
	return file.Entries, nil
}

func (s *AvailabilityStore) write(entries []Unavailability) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Agent < entries[j].Agent })
	data, err := json.MarshalIndent(availabilityFile{Version: availabilitySchemaVersion, Entries: entries}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".availability-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceAvailabilityFile(tmpName, s.path)
}

func active(entries []Unavailability, now time.Time) []Unavailability {
	result := make([]Unavailability, 0, len(entries))
	for _, entry := range entries {
		if (entry.Agent == "claude" || entry.Agent == "codex") && entry.Expires.After(now) {
			result = append(result, entry)
		}
	}
	return result
}
