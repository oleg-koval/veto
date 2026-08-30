package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFeedbackRedactReportRemovesSecretsAndLocalPaths(t *testing.T) {
	t.Setenv("HOME", "/Users/example")
	report := FeedbackReport{
		Kind:             "bug",
		Summary:          "provider failed",
		Reproduction:     "run with api_key=sk-secret123 from /Users/example/project",
		ExpectedBehavior: "success",
		ActualBehavior:   "Authorization: Bearer ghp_secret987; file ~/.veto/credentials.json",
		Scope:            "run",
		AcceptanceCriteria: []string{
			"No token=secret456 is included",
		},
		Metadata: FeedbackMetadata{ProviderModel: "provider/model"},
	}

	redacted := redactFeedbackReport(report)
	text := mustJSON(t, redacted)
	for _, forbidden := range []string{"sk-secret123", "ghp_secret987", "secret456", "/Users/example", "~/.veto", "credentials.json"} {
		assert.NotContains(t, text, forbidden)
	}
	assert.Contains(t, text, "[redacted]")
	assert.Empty(t, redacted.Metadata.ProviderModel, "provider/model requires explicit opt-in")
}

func TestFeedbackBuildIssueURLIsBoundedAndUsesFormVocabulary(t *testing.T) {
	report := FeedbackReport{
		Kind:               "bug",
		Summary:            strings.Repeat("long summary ", 700),
		Reproduction:       "steps",
		ExpectedBehavior:   "expected",
		ActualBehavior:     "actual",
		Scope:              "scope",
		AcceptanceCriteria: []string{"criterion"},
		Metadata:           FeedbackMetadata{Command: "run", Risk: "medium"},
	}

	issueURL, truncated := buildFeedbackIssueURL(report)
	assert.LessOrEqual(t, len(issueURL), maxFeedbackURLLength)
	assert.True(t, truncated)
	assert.Contains(t, issueURL, "template=bug_report.yml")
	assert.Contains(t, issueURL, "labels=bug")
}

func TestFeedbackInteractiveCollectorUsesFakeInput(t *testing.T) {
	input := strings.NewReader("1\nsummary\nreproduce\nexpected\nactual\nscope\ncriterion\nnot assessed\nn\n")
	var output strings.Builder

	_, err := collectInteractiveFeedback(input, &output, FeedbackReport{})
	require.Error(t, err)
	assert.Contains(t, output.String(), "Submit this redacted report")
}

func TestFeedbackWriteReportUsesPrivatePermissions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path, err := writeFeedbackReport(FeedbackReport{Kind: "bug", Summary: "test"})
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func TestRunFeedbackJSONInputExcludesProviderWithoutOptIn(t *testing.T) {
	reportDir := t.TempDir()
	feedbackPathOverride = reportDir
	t.Cleanup(func() { feedbackPathOverride = "" })
	input := `{"kind":"feature","summary":"add report","reproduction_context":"current workflow","expected_behavior":"report is saved","actual_behavior":"no command exists","scope":"CLI","acceptance_criteria":["tests pass"],"metadata":{"provider_model":"secret/provider","command":"veto run --private value"}}`
	result, err := runFeedback([]string{"--stdin", "--json", "--no-browser"}, strings.NewReader(input), &strings.Builder{}, &strings.Builder{}, nil)
	require.NoError(t, err)
	assert.Empty(t, result.Report.Metadata.ProviderModel)
	assert.Equal(t, "veto", result.Report.Metadata.Command)
	assert.NotEmpty(t, result.IssueURL)
}

func TestPostRunFeedbackIsDisabledByDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	assert.False(t, postRunFeedbackEnabled())
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	b, err := json.Marshal(value)
	require.NoError(t, err)
	return string(b)
}
