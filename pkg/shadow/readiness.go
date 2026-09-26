package shadow

import "math"

const PromotionPolicyVersion = 1

type GateStatus string

const (
	GatePass             GateStatus = "pass"
	GateFail             GateStatus = "fail"
	GateInsufficientData GateStatus = "insufficient_data"
)

type ReadinessStatus string

const (
	ReadinessReady        ReadinessStatus = "ready_for_opt_in_experiment"
	ReadinessNotReady     ReadinessStatus = "not_ready"
	ReadinessInsufficient ReadinessStatus = "insufficient_data"
)

// ReadinessGate keeps the observed value and sample count beside its result.
// Threshold is a short machine expression, not a claim of statistical proof.
type ReadinessGate struct {
	Status    GateStatus `json:"status"`
	Observed  float64    `json:"observed,omitempty"`
	Samples   int        `json:"samples,omitempty"`
	Threshold string     `json:"threshold"`
}

// ReadinessReport applies promotion policy v1. Passing means only that v0.15
// may begin as an opt-in experiment; it never enables hybrid routing.
type ReadinessReport struct {
	PolicyVersion int                      `json:"policy_version"`
	Status        ReadinessStatus          `json:"status"`
	Gates         map[string]ReadinessGate `json:"gates"`
}

func evaluateReadiness(dataset Dataset, report Report, fallbackConfidence float64) ReadinessReport {
	gates := map[string]ReadinessGate{}
	coveragePass := report.LabeledRoutes >= 500 && len(report.LabeledRoutesByTaskKind) >= 5
	for _, count := range report.LabeledRoutesByTaskKind {
		coveragePass = coveragePass && count >= 30
	}
	gates["labeled_coverage"] = booleanGate(coveragePass, coveragePass, float64(report.LabeledRoutes), report.LabeledRoutes, ">=500 labels, >=5 kinds, >=30 each")

	direct, directByKind := pairedDifferences(dataset, false, fallbackConfidence)
	lower := confidenceLowerBound(direct)
	pairedCoverageEnough := report.LabeledRoutes > 0 && len(direct)*100 >= report.LabeledRoutes*95
	successPass := len(direct) >= 500 && pairedCoverageEnough && lower >= -.02
	perKindEnough := len(directByKind) >= 5
	perKindPass := true
	for _, values := range directByKind {
		perKindEnough = perKindEnough && len(values) >= 30
		perKindPass = perKindPass && mean(values) >= -.05
	}
	gates["routing_success_noninferiority"] = numericGate(successPass && perKindPass, len(direct) >= 500 && pairedCoverageEnough && perKindEnough, lower, len(direct), "paired labels >=95% of labeled routes; 95% lower bound >=-0.02; each kind mean >=-0.05")

	calibrationEnough := report.Shadow.Calibration.Samples >= 500 && report.Authority.Calibration.Samples >= 500 && report.Shadow.Calibration.Samples*100 >= report.LabeledRoutes*95
	calibrationPass := report.Shadow.Calibration.BrierScore <= report.Authority.Calibration.BrierScore && report.Shadow.Calibration.ECE <= .05
	gates["calibration"] = numericGate(calibrationPass, calibrationEnough, report.Shadow.Calibration.ECE, report.Shadow.Calibration.Samples, "paired coverage >=95%; shadow Brier <= authority proxy and ECE <=0.05")

	latencyEnough := report.Routes > 0 && report.Shadow.LatencyMs.Known*100 >= report.Routes*95
	gates["latency"] = numericGate(report.Shadow.LatencyMs.P95 <= 750, latencyEnough, report.Shadow.LatencyMs.P95, report.Shadow.LatencyMs.Known, "known >=95%; p95_ms <=750")
	gates["availability"] = numericGate(report.ShadowUnavailableOrError.Value <= .01, report.Routes >= 500, report.ShadowUnavailableOrError.Value, report.Routes, "unavailable_or_error <=0.01")

	costEnough := report.Routes > 0 && report.Authority.CostUSD.Known*100 >= report.Routes*95 && report.Shadow.CostUSD.Known*100 >= report.Routes*95 && report.Authority.CostUSD.Average > 0
	costRatio := 0.0
	if report.Authority.CostUSD.Average > 0 {
		costRatio = report.Shadow.CostUSD.Average / report.Authority.CostUSD.Average
	}
	gates["decision_cost"] = numericGate(costRatio <= .10+1e-12, costEnough, costRatio, min(report.Authority.CostUSD.Known, report.Shadow.CostUSD.Known), "known >=95%; shadow/authority average <=0.10")

	hybrid, hybridByKind := pairedDifferences(dataset, true, fallbackConfidence)
	hybridLower := confidenceLowerBound(hybrid)
	hybridCoverageEnough := report.LabeledRoutes > 0 && len(hybrid)*100 >= report.LabeledRoutes*95
	hybridEnough := len(hybrid) >= 500 && hybridCoverageEnough && len(hybridByKind) >= 5
	hybridPass := report.SimulatedFallback.Value <= .20 && hybridLower >= -.02
	for _, values := range hybridByKind {
		hybridEnough = hybridEnough && len(values) >= 30
		hybridPass = hybridPass && mean(values) >= -.05
	}
	gates["simulated_hybrid"] = numericGate(hybridPass, hybridEnough, report.SimulatedFallback.Value, len(hybrid), "paired labels >=95%; fallback <=0.20 and success noninferiority passes")

	// These are structural properties of the v1 DTO and distinct Go contracts,
	// backed by tests rather than inferred from traffic.
	gates["privacy_schema"] = ReadinessGate{Status: GatePass, Observed: 1, Samples: 1, Threshold: "v1 schema excludes raw objectives, credentials, bodies, details"}
	gates["zero_routing_influence"] = ReadinessGate{Status: GatePass, Observed: 1, Samples: 1, Threshold: "ShadowDecider cannot return DecisionOutcome"}

	status := ReadinessReady
	for _, gate := range gates {
		if gate.Status == GateFail {
			status = ReadinessNotReady
			break
		}
		if gate.Status == GateInsufficientData {
			status = ReadinessInsufficient
		}
	}
	return ReadinessReport{PolicyVersion: PromotionPolicyVersion, Status: status, Gates: gates}
}

func booleanGate(pass, enough bool, observed float64, samples int, threshold string) ReadinessGate {
	return numericGate(pass, enough, observed, samples, threshold)
}

func numericGate(pass, enough bool, observed float64, samples int, threshold string) ReadinessGate {
	status := GateInsufficientData
	if enough {
		status = GateFail
		if pass {
			status = GatePass
		}
	}
	return ReadinessGate{Status: status, Observed: observed, Samples: samples, Threshold: threshold}
}

func pairedDifferences(dataset Dataset, hybrid bool, threshold float64) ([]float64, map[string][]float64) {
	var all []float64
	byKind := make(map[string][]float64)
	for _, route := range dataset.Routes {
		authority, authorityKnown := decisionSuccess(route, route.Comparison.Authority)
		if !authorityKnown {
			continue
		}
		selected := route.Comparison.Shadow
		if hybrid && shouldFallback(route.Comparison, threshold) {
			selected = route.Comparison.Authority
		}
		candidate, candidateKnown := decisionSuccess(route, selected)
		if !candidateKnown {
			continue
		}
		difference := candidate - authority
		all = append(all, difference)
		byKind[route.Comparison.TaskKind] = append(byKind[route.Comparison.TaskKind], difference)
	}
	return all, byKind
}

func decisionSuccess(route RouteRecord, decision DecisionEvidence) (float64, bool) {
	if decision.Status != StatusSelected {
		return 0, decision.Status == StatusNoSelection || decision.Status == StatusError || decision.Status == StatusUnavailable || decision.Status == StatusTimeout || decision.Status == StatusCanceled || decision.Status == StatusMalformed
	}
	label, ok := route.Labels[decision.SelectedCandidate]
	if !ok || !label.Success.Known {
		return 0, false
	}
	if label.Success.Value {
		return 1, true
	}
	return 0, true
}

func confidenceLowerBound(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	average := mean(values)
	var squares float64
	for _, value := range values {
		delta := value - average
		squares += delta * delta
	}
	standardError := math.Sqrt((squares / float64(len(values)-1)) / float64(len(values)))
	return average - 1.96*standardError
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var total float64
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}
