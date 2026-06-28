package main

import (
	"encoding/json"
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
	return c, json.Unmarshal(data, &c)
}

func saveCredential(envKey, value string) error {
	path := credentialsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	c, err := loadCredentials()
	if err != nil {
		c = credentials{}
	}
	c[envKey] = value
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
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
	return os.WriteFile(path, data, 0600)
}

// getKey returns the API key for envKey — env var wins, then credentials file.
func getKey(envKey string, creds credentials) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return creds[envKey]
}

