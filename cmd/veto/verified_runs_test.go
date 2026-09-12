package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oleg-koval/veto/pkg/router"
	"github.com/oleg-koval/veto/pkg/verifiedrun"
	"github.com/stretchr/testify/require"
)

func TestVerifiedReceiptStoreRoundTripAndDelete(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	receipt := verifiedrun.Receipt{RunID: "run-one", TaskID: "task-one", CreatedAt: time.Now(), Outcome: verifiedrun.OutcomeVerifiedPass}
	require.NoError(t, saveVerifiedReceipt(receipt))
	receipts := readVerifiedReceipts()
	require.Len(t, receipts, 1)
	require.Equal(t, receipt.RunID, receipts[0].RunID)
	dir, err := verifiedReceiptDir()
	require.NoError(t, err)
	info, err := os.Stat(filepath.Join(dir, "run-one.json"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	require.NoError(t, deleteVerifiedReceipts(map[string]struct{}{"run-one": {}}))
	require.Empty(t, readVerifiedReceipts())
}

func TestParseVerificationFiles(t *testing.T) {
	dir := t.TempDir()
	criteriaPath := filepath.Join(dir, "criteria.json")
	evidencePath := filepath.Join(dir, "evidence.json")
	require.NoError(t, os.WriteFile(criteriaPath, []byte(`{"version":1,"criteria":["tests pass"]}`), 0600))
	require.NoError(t, os.WriteFile(evidencePath, []byte(`{"version":1,"evidence":[{"id":"test","criterion":"tests pass","type":"test","summary":"all passed"}]}`), 0600))
	criteria, err := parseCriteriaFile(criteriaPath)
	require.NoError(t, err)
	evidence, err := parseEvidenceFile(evidencePath, criteria)
	require.NoError(t, err)
	require.Len(t, evidence, 1)
}

func TestVerifiedRunReportRequiresCompleteCostCoverage(t *testing.T) {
	report := buildVerifiedRunReport([]verifiedrun.Receipt{
		{Outcome: verifiedrun.OutcomeVerifiedPass, EvidenceCoverage: 1, EvidenceTotal: 1, ExecutionCostKnown: true, ExecutionCostUSD: 0.25},
		{Outcome: verifiedrun.OutcomeInconclusive, EvidenceCoverage: 1, EvidenceTotal: 1},
	})
	require.Equal(t, 1, report.VerifiedPass)
	require.Equal(t, 1, report.UnknownCostRuns)
	require.Nil(t, report.CostPerVerifiedRun)
}

func TestPersistVerifiedReceiptRedactsCriteriaAndSetsVersion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workdir := t.TempDir()
	previousWorkdir, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workdir))
	t.Cleanup(func() { require.NoError(t, os.Chdir(previousWorkdir)) })
	persistVerifiedReceipt(
		router.TaskSpec{ID: "task", SuccessCriteria: []string{"token=sk-abcdefghi"}},
		router.ModelCapabilities{Name: "model"},
		router.ExecutionMetrics{},
		[]verifiedrun.Evidence{{ID: "evidence", Criterion: "token=sk-abcdefghi", Type: "test", Summary: "passed"}},
		ReviewResult{}, verifiedrun.OutcomeInconclusive, "receipt.json",
	)

	receipts := readVerifiedReceipts()
	require.Len(t, receipts, 1)
	require.Equal(t, verifiedrun.SchemaVersion, receipts[0].Version)
	require.NotContains(t, receipts[0].Criteria[0].Criterion, "sk-abcdefghi")
	require.True(t, strings.Contains(receipts[0].Criteria[0].Criterion, "[REDACTED]"))

	data, err := os.ReadFile(filepath.Join(workdir, "receipt.json"))
	require.NoError(t, err)
	var exported verifiedrun.Receipt
	require.NoError(t, json.Unmarshal(data, &exported))
	require.Equal(t, verifiedrun.SchemaVersion, exported.Version)
	require.NotContains(t, exported.Criteria[0].Criterion, "sk-abcdefghi")
}
