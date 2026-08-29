package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/oleg-koval/veto/pkg/router"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogEventIncludesProviderErrorDetail(t *testing.T) {
	var output bytes.Buffer
	previous := routeLog
	routeLog = slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() { routeLog = previous })

	logEvent("plan a QA pass", "plan", "low", router.ProgressEvent{
		Kind:   router.EventAskError,
		Model:  "sol",
		Detail: "openai api: unsupported parameter",
	})

	var event map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &event))
	assert.Equal(t, "openai api: unsupported parameter", event["detail"])
}
