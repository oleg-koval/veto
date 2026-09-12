package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/oleg-koval/veto/pkg/verifiedrun"
)

const maxVerifiedReceipts = 500

func verifiedReceiptDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".veto", "receipts"), nil
}

func saveVerifiedReceipt(receipt verifiedrun.Receipt) error {
	if strings.TrimSpace(receipt.RunID) == "" || strings.TrimSpace(receipt.TaskID) == "" {
		return fmt.Errorf("verified receipt requires run and task identity")
	}
	dir, err := verifiedReceiptDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create receipt directory: %w", err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return fmt.Errorf("protect receipt directory: %w", err)
	}
	receipt.Version = verifiedrun.SchemaVersion
	receipt.CreatedAt = receipt.CreatedAt.UTC()
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("encode verified receipt: %w", err)
	}
	path := filepath.Join(dir, receipt.RunID+".json")
	temporary, err := os.CreateTemp(dir, ".receipt-*.tmp")
	if err != nil {
		return fmt.Errorf("create verified receipt: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect verified receipt: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write verified receipt: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close verified receipt: %w", err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("replace verified receipt: %w", err)
	}
	return trimVerifiedReceipts(dir)
}

func trimVerifiedReceipts(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	type entry struct {
		name string
		mod  time.Time
	}
	items := make([]entry, 0, len(entries))
	for _, item := range entries {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".json") {
			continue
		}
		info, err := item.Info()
		if err == nil {
			items = append(items, entry{name: item.Name(), mod: info.ModTime()})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mod.Before(items[j].mod) })
	for len(items) > maxVerifiedReceipts {
		if err := os.Remove(filepath.Join(dir, items[0].name)); err != nil {
			return err
		}
		items = items[1:]
	}
	return nil
}

func readVerifiedReceipts() []verifiedrun.Receipt {
	dir, err := verifiedReceiptDir()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	receipts := make([]verifiedrun.Receipt, 0, len(entries))
	for _, item := range entries {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, item.Name()))
		if err != nil {
			continue
		}
		var receipt verifiedrun.Receipt
		if json.Unmarshal(data, &receipt) != nil || receipt.Version != verifiedrun.SchemaVersion || receipt.RunID == "" {
			continue
		}
		receipts = append(receipts, receipt)
	}
	sort.Slice(receipts, func(i, j int) bool { return receipts[i].CreatedAt.After(receipts[j].CreatedAt) })
	return receipts
}

func deleteVerifiedReceipts(runIDs map[string]struct{}) error {
	dir, err := verifiedReceiptDir()
	if err != nil {
		return err
	}
	for runID := range runIDs {
		if err := os.Remove(filepath.Join(dir, runID+".json")); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func parseCriteriaFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read criteria file: %w", err)
	}
	return verifiedrun.ParseCriteria(data)
}

func parseEvidenceFile(path string, criteria []string) ([]verifiedrun.Evidence, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read evidence file: %w", err)
	}
	return verifiedrun.ParseEvidence(data, criteria)
}

type verifiedRunReport struct {
	Total              int      `json:"total"`
	VerifiedPass       int      `json:"verified_pass"`
	VerifiedFail       int      `json:"verified_fail"`
	Inconclusive       int      `json:"inconclusive"`
	EvidenceCoverage   float64  `json:"evidence_coverage"`
	KnownCostRuns      int      `json:"known_execution_cost_runs"`
	UnknownCostRuns    int      `json:"unknown_execution_cost_runs"`
	CostPerVerifiedRun *float64 `json:"execution_cost_per_verified_pass,omitempty"`
}

func cmdVerifiedRuns(args []string) {
	if len(args) == 0 || args[0] == "list" {
		for _, receipt := range readVerifiedReceipts() {
			fmt.Printf("%s  %s  %s  %s\n", receipt.CreatedAt.Local().Format("2006-01-02 15:04"), receipt.Outcome, receipt.Model, receipt.RunID)
		}
		return
	}
	if args[0] != "report" {
		fmt.Fprintln(os.Stderr, "usage: veto verified-runs [list|report --json]")
		return
	}
	fs := flag.NewFlagSet("verified-runs report", flag.ExitOnError)
	jsonOutput := fs.Bool("json", false, "emit JSON")
	_ = fs.Parse(args[1:])
	report := buildVerifiedRunReport(readVerifiedReceipts())
	if *jsonOutput {
		data, _ := json.Marshal(report)
		fmt.Println(string(data))
		return
	}
	fmt.Printf("verified runs: %d total · %d pass · %d fail · %d inconclusive\n", report.Total, report.VerifiedPass, report.VerifiedFail, report.Inconclusive)
	fmt.Printf("evidence coverage: %.0f%% · execution cost coverage: %d known, %d unknown\n", report.EvidenceCoverage*100, report.KnownCostRuns, report.UnknownCostRuns)
	if report.CostPerVerifiedRun == nil {
		fmt.Println("execution cost per verified pass: insufficient evidence")
	} else {
		fmt.Printf("execution cost per verified pass: $%.6f\n", *report.CostPerVerifiedRun)
	}
}

func buildVerifiedRunReport(receipts []verifiedrun.Receipt) verifiedRunReport {
	report := verifiedRunReport{Total: len(receipts)}
	covered, criteria := 0, 0
	knownCost := 0.0
	for _, receipt := range receipts {
		switch receipt.Outcome {
		case verifiedrun.OutcomeVerifiedPass:
			report.VerifiedPass++
		case verifiedrun.OutcomeVerifiedFail:
			report.VerifiedFail++
		default:
			report.Inconclusive++
		}
		covered += receipt.EvidenceCoverage
		criteria += receipt.EvidenceTotal
		if receipt.ExecutionCostKnown {
			report.KnownCostRuns++
			knownCost += receipt.ExecutionCostUSD
		} else {
			report.UnknownCostRuns++
		}
	}
	if criteria > 0 {
		report.EvidenceCoverage = float64(covered) / float64(criteria)
	}
	// A cost-per-pass is only trustworthy when every stored run reports an
	// execution cost. Admission and review costs remain explicitly out of scope.
	if report.VerifiedPass > 0 && report.UnknownCostRuns == 0 {
		value := knownCost / float64(report.VerifiedPass)
		report.CostPerVerifiedRun = &value
	}
	return report
}
