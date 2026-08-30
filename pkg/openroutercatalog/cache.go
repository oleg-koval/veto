package openroutercatalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const cacheVersion = 1

type persistedCache struct {
	Version   int       `json:"version"`
	FetchedAt time.Time `json:"fetched_at"`
	ETag      string    `json:"etag,omitempty"`
	Models    []Model   `json:"models"`
}

// snapshotFromCache creates a snapshot from persisted cache data and determines whether it is fresh based on its age.
// The snapshot preserves the cached models, fetch timestamp, ETag, and offline status.
func snapshotFromCache(cache persistedCache, now time.Time, maxAge time.Duration, offline bool) Snapshot {
	state := StateStale
	age := now.Sub(cache.FetchedAt)
	if age >= 0 && age <= maxAge {
		state = StateFresh
	}
	return Snapshot{
		Models: cache.Models, FetchedAt: cache.FetchedAt, ETag: cache.ETag,
		State: state, Offline: offline,
	}
}

// readCacheFile reads and validates a persisted cache file.
// It returns the cached data or an error if the file is missing, inaccessible, oversized, malformed, or contains invalid models.
func readCacheFile(path string, maxBytes int64) (persistedCache, error) {
	if path == "" {
		return persistedCache{}, errors.New("cache path is empty")
	}
	if pathHasSymlink(path) {
		return persistedCache{}, errors.New("cache path contains a symbolic link")
	}
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return persistedCache{}, fs.ErrNotExist
		}
		return persistedCache{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return persistedCache{}, errors.New("cache is not a regular file")
	}
	if info.Size() > maxBytes {
		return persistedCache{}, errors.New("cache is too large")
	}
	file, err := os.Open(path)
	if err != nil {
		return persistedCache{}, err
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || int64(len(body)) > maxBytes {
		return persistedCache{}, errors.New("cache is unreadable")
	}
	var cache persistedCache
	if err := json.Unmarshal(body, &cache); err != nil || cache.Version != cacheVersion || cache.FetchedAt.IsZero() || len(cache.ETag) > 512 {
		return persistedCache{}, errors.New("cache is malformed")
	}
	models, err := validateStoredModels(cache.Models)
	if err != nil {
		return persistedCache{}, errors.New("cache is malformed")
	}
	cache.Models = models
	return cache, nil
}

// validateStoredModels validates and normalizes persisted models, ensuring their identifiers, metadata, pricing, modalities, parameters, and status-dependent expiration values are valid.
func validateStoredModels(models []Model) ([]Model, error) {
	if len(models) == 0 || len(models) > maxCatalogModels {
		return nil, errors.New("invalid model count")
	}
	seen := make(map[string]bool, len(models))
	for i := range models {
		model := &models[i]
		if strings.TrimSpace(model.ID) != model.ID || strings.TrimSpace(model.Name) != model.Name ||
			model.ID == "" || model.Name == "" || len(model.ID) > 512 || len(model.Name) > 512 ||
			hasControl(model.ID) || hasControl(model.Name) || seen[model.ID] ||
			(model.Status != StatusAvailable && model.Status != StatusScheduledForRemoval) ||
			(model.ContextLength != nil && (*model.ContextLength <= 0 || *model.ContextLength > 100_000_000)) ||
			!validStoredPrice(model.PromptUSDPerToken) || !validStoredPrice(model.CompletionUSDPerToken) {
			return nil, errors.New("invalid model")
		}
		var err error
		model.InputModalities, err = normalizeStrings(model.InputModalities)
		if err != nil {
			return nil, err
		}
		model.OutputModalities, err = normalizeStrings(model.OutputModalities)
		if err != nil {
			return nil, err
		}
		model.SupportedParameters, err = normalizeStrings(model.SupportedParameters)
		if err != nil {
			return nil, err
		}
		if model.Status == StatusScheduledForRemoval && !validExpirationDate(model.ExpirationDate) {
			return nil, errors.New("invalid expiration date")
		}
		if model.Status == StatusAvailable && model.ExpirationDate != "" {
			return nil, errors.New("inconsistent model status")
		}
		seen[model.ID] = true
	}
	return models, nil
}

// validStoredPrice reports whether a stored price is absent or finite and greater than or equal to zero.
func validStoredPrice(price *float64) bool {
	return price == nil || (!math.IsNaN(*price) && !math.IsInf(*price, 0) && *price >= 0)
}

// writeCacheFile validates and atomically writes persisted cache data to path.
func writeCacheFile(path string, cache persistedCache) error {
	if path == "" {
		return errors.New("cache path is empty")
	}
	if pathHasSymlink(path) {
		return errors.New("cache path contains a symbolic link")
	}
	if cache.Version != cacheVersion || cache.FetchedAt.IsZero() || len(cache.ETag) > 512 {
		return errors.New("cache is malformed")
	}
	models, err := validateStoredModels(cache.Models)
	if err != nil {
		return errors.New("cache is malformed")
	}
	cache.Models = models
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("cache target is not a regular file")
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	body, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	if len(body) > defaultMaxResponseBytes {
		return errors.New("cache is too large")
	}
	temp, err := os.CreateTemp(dir, ".openrouter-models-*.json")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(append(body, '\n')); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := replaceCacheFile(tempPath, path); err != nil {
		return err
	}
	return nil
}

// pathHasSymlink reports whether the path or either of its two parent directories is a symbolic link or cannot be inspected.
func pathHasSymlink(path string) bool {
	clean := filepath.Clean(path)
	for _, candidate := range []string{clean, filepath.Dir(clean), filepath.Dir(filepath.Dir(clean))} {
		info, err := os.Lstat(candidate)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

// replaceCacheFile atomically replaces the cache file at path with the temporary file at tempPath.
func replaceCacheFile(tempPath, path string) error {
	if err := os.Rename(tempPath, path); err == nil {
		return nil
	}
	if _, err := os.Lstat(path); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	backup, err := os.CreateTemp(dir, ".openrouter-backup-*")
	if err != nil {
		return err
	}
	backupPath := backup.Name()
	if err := backup.Close(); err != nil {
		os.Remove(backupPath)
		return err
	}
	if err := os.Remove(backupPath); err != nil {
		return err
	}
	if err := os.Rename(path, backupPath); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		if rollbackErr := os.Rename(backupPath, path); rollbackErr != nil {
			return fmt.Errorf("replace cache: %w; rollback failed: %v", err, rollbackErr)
		}
		return err
	}
	_ = os.Remove(backupPath)
	return nil
}
