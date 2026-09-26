package shadowhistory

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oleg-koval/veto/pkg/router"
	shadowdata "github.com/oleg-koval/veto/pkg/shadow"
	"github.com/stretchr/testify/require"
)

// TestFileRecorderWritesPrivateValidatedJSONL checks private file permissions and evidence round trips.
func TestFileRecorderWritesPrivateValidatedJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "evidence.jsonl")
	recorder := NewFileRecorder(path, 3)
	comparison := router.ShadowComparisonRecord{
		RouteID: "r-1", ObservedAt: time.Unix(1, 0).UTC(), TaskKind: router.KindPlan, Risk: router.RiskMedium,
		Candidates: []string{"c-1"}, AuthorityStrategy: "sequential-admission", ShadowStrategy: "jev:test",
		Authority: router.ShadowDecisionRecord{Status: router.ShadowStatusSelected, SelectedCandidate: "c-1"},
		Shadow:    router.ShadowDecisionRecord{Status: router.ShadowStatusUnavailable, ErrorCode: "UNAVAILABLE"},
	}
	require.NoError(t, recorder.RecordShadowComparison(comparison))
	require.NoError(t, recorder.RecordShadowExecutionLabel(router.ShadowExecutionLabelRecord{
		RouteID: "r-1", ObservedAt: time.Unix(2, 0).UTC(), Candidate: "c-1",
		Success: router.ShadowKnownBool{Known: true, Value: true}, Score: router.DecisionProbability{Known: true, Value: 1},
	}))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	file, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, file.Close()) })
	events, err := shadowdata.Load(file)
	require.NoError(t, err)
	require.Len(t, events, 2)
	dataset, err := shadowdata.Materialize(events)
	require.NoError(t, err)
	require.True(t, dataset.Routes[0].Labels["c-1"].Success.Value)
}

// TestFileRecorderStopsAtBound checks that the recorder rejects appends beyond the event limit.
func TestFileRecorderStopsAtBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evidence.jsonl")
	recorder := NewFileRecorder(path, 1)
	record := router.ShadowComparisonRecord{
		RouteID: "r-1", ObservedAt: time.Unix(1, 0).UTC(), TaskKind: router.KindPlan, Risk: router.RiskMedium,
		Candidates: []string{"c-1"}, AuthorityStrategy: "sequential-admission", ShadowStrategy: "jev:test",
		Authority: router.ShadowDecisionRecord{Status: router.ShadowStatusNoSelection},
		Shadow:    router.ShadowDecisionRecord{Status: router.ShadowStatusNoSelection},
	}
	require.NoError(t, recorder.RecordShadowComparison(record))
	record.RouteID = "r-2"
	require.ErrorIs(t, recorder.RecordShadowComparison(record), ErrEvidenceLimit)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, 1, bytes.Count(data, []byte{'\n'}))
}

// TestFileRecorderDoesNotOverwriteMalformedEvidence checks that invalid existing evidence is preserved.
func TestFileRecorderDoesNotOverwriteMalformedEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evidence.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("not json\n"), 0600))
	recorder := NewFileRecorder(path, 2)
	record := router.ShadowComparisonRecord{
		RouteID: "r-1", ObservedAt: time.Unix(1, 0).UTC(), TaskKind: router.KindPlan, Risk: router.RiskMedium,
		Candidates: []string{"c-1"}, AuthorityStrategy: "sequential-admission", ShadowStrategy: "jev:test",
		Authority: router.ShadowDecisionRecord{Status: router.ShadowStatusNoSelection},
		Shadow:    router.ShadowDecisionRecord{Status: router.ShadowStatusNoSelection},
	}
	err := recorder.RecordShadowComparison(record)
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrEvidenceLimit))
	record.RouteID = "r-2"
	require.Error(t, recorder.RecordShadowComparison(record), "every append must fail closed while existing evidence is malformed")
	data, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	require.Equal(t, "not json\n", string(data))
}

// TestFileRecorderFailsClosedAfterInvalidEvent checks that validation failure disables later appends.
func TestFileRecorderFailsClosedAfterInvalidEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evidence.jsonl")
	recorder := NewFileRecorder(path, 2)
	invalid := router.ShadowComparisonRecord{
		RouteID: "", ObservedAt: time.Unix(1, 0).UTC(), TaskKind: router.KindPlan, Risk: router.RiskMedium,
		Candidates: []string{"c-1"}, Authority: router.ShadowDecisionRecord{Status: router.ShadowStatusNoSelection},
		Shadow: router.ShadowDecisionRecord{Status: router.ShadowStatusNoSelection},
	}
	require.Error(t, recorder.RecordShadowComparison(invalid))
	invalid.RouteID = "r-1"
	require.Error(t, recorder.RecordShadowComparison(invalid))
	_, err := os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}
