package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/oleg-koval/veto/pkg/router"
)

// LocalModel holds the user-supplied definition of a self-hosted model.
type LocalModel struct {
	Name             string   `json:"name"`                         // routing id (unique)
	Endpoint         string   `json:"endpoint"`                     // full chat-completions URL
	Model            string   `json:"model"`                        // server-side model id
	APIKey           string   `json:"api_key,omitempty"`
	Tier             string   `json:"tier,omitempty"`               // default: "small"
	MaxContextTokens int      `json:"max_context_tokens,omitempty"` // default: 8192
	SupportsTools    []string `json:"supports_tools,omitempty"`     // default: {bash,read,write,edit}
	Strengths        []string `json:"strengths,omitempty"`
	Weaknesses       []string `json:"weaknesses,omitempty"`
}

// localModelsPathOverride lets tests redirect the models file to a temp path.
var localModelsPathOverride string

func localModelsPath() string {
	if localModelsPathOverride != "" {
		return localModelsPathOverride
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".veto", "models.json")
}

func loadLocalModels() ([]LocalModel, error) {
	data, err := os.ReadFile(localModelsPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var models []LocalModel
	return models, json.Unmarshal(data, &models)
}

func saveLocalModel(lm LocalModel) error {
	models, err := loadLocalModels()
	if err != nil {
		models = nil
	}
	// replace by name if already present
	replaced := false
	for i, m := range models {
		if m.Name == lm.Name {
			models[i] = lm
			replaced = true
			break
		}
	}
	if !replaced {
		models = append(models, lm)
	}
	data, err := json.MarshalIndent(models, "", "  ")
	if err != nil {
		return err
	}
	path := localModelsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func removeLocalModel(name string) error {
	models, err := loadLocalModels()
	if err != nil {
		return err
	}
	filtered := models[:0]
	found := false
	for _, m := range models {
		if m.Name == name {
			found = true
			continue
		}
		filtered = append(filtered, m)
	}
	if !found {
		fmt.Printf("  Local model %q is not configured — nothing to remove.\n", name)
		return nil
	}
	data, err := json.MarshalIndent(filtered, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(localModelsPath(), data, 0600)
}

// capabilities converts a LocalModel to the router type, applying defaults.
func (lm LocalModel) capabilities() router.ModelCapabilities {
	tier := lm.Tier
	if tier == "" {
		tier = "small"
	}
	ctx := lm.MaxContextTokens
	if ctx == 0 {
		ctx = 8192
	}
	tools := lm.SupportsTools
	if len(tools) == 0 {
		tools = []string{"bash", "read", "write", "edit"}
	}
	var strengths []router.TaskKind
	for _, s := range lm.Strengths {
		strengths = append(strengths, router.TaskKind(s))
	}
	var weaknesses []router.TaskKind
	for _, w := range lm.Weaknesses {
		weaknesses = append(weaknesses, router.TaskKind(w))
	}
	return router.ModelCapabilities{
		Name:             lm.Name,
		Tier:             tier,
		MaxContextTokens: ctx,
		SupportsTools:    tools,
		Strengths:        strengths,
		Weaknesses:       weaknesses,
		// CostPer1k*USD = 0: local inference, no per-token billing
	}
}

// validateLocalModel returns an error if the model definition is not usable.
func validateLocalModel(lm LocalModel, existingNames map[string]bool) error {
	if lm.Name == "" {
		return fmt.Errorf("name is required")
	}
	if lm.Endpoint == "" {
		return fmt.Errorf("endpoint is required")
	}
	if lm.Model == "" {
		return fmt.Errorf("model id is required")
	}
	if _, err := url.ParseRequestURI(lm.Endpoint); err != nil {
		return fmt.Errorf("endpoint %q is not a valid URL: %w", lm.Endpoint, err)
	}
	if existingNames[lm.Name] {
		return fmt.Errorf("name %q conflicts with a built-in model; choose a different name", lm.Name)
	}
	return nil
}
