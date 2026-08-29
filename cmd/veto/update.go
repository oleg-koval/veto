package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const updateCheckInterval = 24 * time.Hour

var stableVersionPattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`)

type updateCache struct {
	CheckedAt       time.Time `json:"checked_at"`
	LatestVersion   string    `json:"latest_version"`
	PromptedAt      time.Time `json:"prompted_at,omitempty"`
	PromptedVersion string    `json:"prompted_version,omitempty"`
}

type updateCheckDeps struct {
	now            func() time.Time
	currentVersion func() string
	readCache      func() (updateCache, error)
	writeCache     func(updateCache) error
	fetchLatest    func(context.Context) (string, error)
	install        func(context.Context, string) error
}

func shouldOfferAutomaticUpdate(args []string, stdinTTY, stderrTTY bool) bool {
	if !stdinTTY || !stderrTTY {
		return false
	}
	for _, arg := range args {
		name := strings.SplitN(arg, "=", 2)[0]
		if name == "--json" || name == "--quiet" {
			return false
		}
	}
	return true
}

func runAutomaticUpdate(ctx context.Context, input io.Reader, output io.Writer, deps updateCheckDeps) bool {
	now := deps.now()
	currentVersion := deps.currentVersion()
	if _, err := parseStableVersion(currentVersion); err != nil {
		return false
	}
	cache, cacheErr := deps.readCache()
	if cacheErr != nil || !recentUpdateTimestamp(now, cache.CheckedAt) {
		latest, err := deps.fetchLatest(ctx)
		if err != nil {
			return false
		}
		cache.CheckedAt = now
		cache.LatestVersion = latest
		_ = deps.writeCache(cache)
	}

	newer, err := newerStableVersion(currentVersion, cache.LatestVersion)
	if err != nil || !newer {
		return false
	}
	if cache.PromptedVersion == cache.LatestVersion && recentUpdateTimestamp(now, cache.PromptedAt) {
		return false
	}

	fmt.Fprintf(output, "\n  veto %s is available. Update now? [y/N]: ", cache.LatestVersion)
	scanner := bufio.NewScanner(input)
	accepted := scanner.Scan() && strings.EqualFold(strings.TrimSpace(scanner.Text()), "y")
	cache.PromptedAt = now
	cache.PromptedVersion = cache.LatestVersion
	_ = deps.writeCache(cache)
	if !accepted {
		fmt.Fprintln(output)
		return false
	}
	if err := deps.install(ctx, cache.LatestVersion); err != nil {
		fmt.Fprintf(output, "  update failed: %v\n", err)
		return false
	}
	fmt.Fprintf(output, "  updated to veto %s; re-run your command to use it\n", cache.LatestVersion)
	return true
}

func recentUpdateTimestamp(now, timestamp time.Time) bool {
	return !timestamp.IsZero() && !timestamp.After(now) && now.Sub(timestamp) < updateCheckInterval
}

func newerStableVersion(current, latest string) (bool, error) {
	currentParts, err := parseStableVersion(current)
	if err != nil {
		return false, err
	}
	latestParts, err := parseStableVersion(latest)
	if err != nil {
		return false, err
	}
	for i := range currentParts {
		if latestParts[i] != currentParts[i] {
			return latestParts[i] > currentParts[i], nil
		}
	}
	return false, nil
}

func parseStableVersion(version string) ([3]uint64, error) {
	var parsed [3]uint64
	parts := stableVersionPattern.FindStringSubmatch(strings.TrimPrefix(version, "v"))
	if parts == nil {
		return parsed, errors.New("version is not stable semantic version")
	}
	for i := range parsed {
		value, err := strconv.ParseUint(parts[i+1], 10, 64)
		if err != nil {
			return parsed, errors.New("version component is invalid")
		}
		parsed[i] = value
	}
	return parsed, nil
}
