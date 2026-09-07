package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type credentials map[string]string

func credentialsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".veto", "credentials.json")
}

func loadCredentials() (credentials, error) {
	path := credentialsPath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return credentials{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c == nil {
		c = credentials{}
	}
	return c, nil
}

func saveCredential(envKey, value string) error {
	path := credentialsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	c, err := loadCredentials()
	if err != nil {
		return fmt.Errorf("read credentials: %w", err)
	}
	c[envKey] = value
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeCredentialsAtomic(path, data)
}

func removeCredential(envKey string) error {
	c, err := loadCredentials()
	if err != nil {
		return err
	}
	delete(c, envKey)
	path := credentialsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeCredentialsAtomic(path, data)
}

func writeCredentialsAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".credentials-*.tmp")
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
	return replacePrivateFile(tmpName, path)
}

// getKey returns the API key for envKey — env var wins, then credentials file.
func getKey(envKey string, creds credentials) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return creds[envKey]
}
