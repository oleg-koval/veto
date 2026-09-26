package shadow

import (
	"math"
	"sort"
)

// DefaultFallbackConfidence is the v0.14 replay threshold. It is evidence for
// a possible hybrid policy, not permission to route with the shadow decision.
const DefaultFallbackConfidence = 0.80

// Rate reports a numerator and denominator explicitly so missing samples are
// distinguishable from a measured zero rate.
type Rate struct {
	Count int     `json:"count"`
	Total int     `json:"total"`
	Value float64 `json:"value"`
}

// Distribution reports only known measurements.
type Distribution struct {
	Known   int     `json:"known"`
	Average float64 `json:"average"`
	P95     float64 `json:"p95"`
}

// Calibration summarizes task-success probabilities for candidates with known
// labels. When an authority has no probability, its decision confidence is an
// explicit baseline proxy. ECE uses ten fixed bins.
type Calibration struct {
	Samples    int     `json:"samples"`
	BrierScore float64 `json:"brier_score"`
	ECE        float64 `json:"expected_calibration_error"`
}

// DecisionMetrics summarizes one decision source without inventing values for
// missing labels or telemetry.
type DecisionMetrics struct {
	Selections  int          `json:"selections"`
	Labeled     int          `json:"labeled_selections"`
	Success     Rate         `json:"success"`
	LatencyMs   Distribution `json:"latency_ms"`
	CostUSD     Distribution `json:"cost_usd"`
	Calibration Calibration  `json:"calibration"`
}

// Report is a deterministic materialization of a shadow JSONL dataset.
type Report struct {
	SchemaVersion               int             `json:"schema_version"`
	Routes                      int             `json:"routes"`
	LabeledRoutes               int             `json:"labeled_routes"`
	LabeledRoutesByTaskKind     map[string]int  `json:"labeled_routes_by_task_kind"`
	SelectionAgreement          Rate            `json:"selection_agreement"`
	ShadowUnavailableOrError    Rate            `json:"shadow_unavailable_or_error"`
	SimulatedFallback           Rate            `json:"simulated_fallback"`
	FallbackConfidenceThreshold float64         `json:"fallback_confidence_threshold"`
	Authority                   DecisionMetrics `json:"authority"`
	Shadow                      DecisionMetrics `json:"shadow"`
	Readiness                   ReadinessReport `json:"readiness"`
}

// Evaluate computes metrics from recorded evidence only. A route is labeled
// when the authority's selected candidate has a known success label. Shadow
// success remains unknown unless its own selected candidate has a label.
func Evaluate(dataset Dataset, fallbackConfidence float64) Report {
	if fallbackConfidence < 0 || fallbackConfidence > 1 || math.IsNaN(fallbackConfidence) {
		fallbackConfidence = DefaultFallbackConfidence
	}
	report := Report{
		SchemaVersion:               SchemaVersion,
		Routes:                      len(dataset.Routes),
		LabeledRoutesByTaskKind:     make(map[string]int),
		FallbackConfidenceThreshold: fallbackConfidence,
	}

	var authoritySamples, shadowSamples []decisionSample
	var authorityLatencies, shadowLatencies, authorityCosts, shadowCosts []float64
	comparable, agreements, shadowFailures, fallbacks := 0, 0, 0, 0

	for _, record := range dataset.Routes {
		comparison := record.Comparison
		authority := comparison.Authority
		shadowDecision := comparison.Shadow

		if authority.Status == StatusSelected {
			report.Authority.Selections++
			if label, ok := record.Labels[authority.SelectedCandidate]; ok && label.Success.Known {
				report.Authority.Labeled++
				report.LabeledRoutes++
				report.LabeledRoutesByTaskKind[comparison.TaskKind]++
				authoritySamples = append(authoritySamples, decisionSample{probability: authority.Probability, confidence: authority.Confidence, success: label.Success})
			}
		}
		if shadowDecision.Status == StatusSelected {
			report.Shadow.Selections++
			if label, ok := record.Labels[shadowDecision.SelectedCandidate]; ok && label.Success.Known {
				report.Shadow.Labeled++
				shadowSamples = append(shadowSamples, decisionSample{probability: shadowDecision.Probability, confidence: shadowDecision.Confidence, success: label.Success})
			}
		}

		if decisionComparable(authority) && decisionComparable(shadowDecision) {
			comparable++
			if sameSelection(authority, shadowDecision) {
				agreements++
			}
		}
		if !decisionComparable(shadowDecision) {
			shadowFailures++
		}
		if shouldFallback(comparison, fallbackConfidence) {
			fallbacks++
		}

		appendTelemetry(authority.Telemetry, &authorityLatencies, &authorityCosts)
		appendTelemetry(shadowDecision.Telemetry, &shadowLatencies, &shadowCosts)
	}

	report.SelectionAgreement = rate(agreements, comparable)
	report.ShadowUnavailableOrError = rate(shadowFailures, len(dataset.Routes))
	report.SimulatedFallback = rate(fallbacks, len(dataset.Routes))
	report.Authority.Success, report.Authority.Calibration = sampleMetrics(authoritySamples)
	report.Shadow.Success, report.Shadow.Calibration = sampleMetrics(shadowSamples)
	report.Authority.LatencyMs = distribution(authorityLatencies)
	report.Shadow.LatencyMs = distribution(shadowLatencies)
	report.Authority.CostUSD = distribution(authorityCosts)
	report.Shadow.CostUSD = distribution(shadowCosts)
	report.Readiness = evaluateReadiness(dataset, report, fallbackConfidence)
	return report
}

type decisionSample struct {
	probability KnownFloat
	confidence  KnownFloat
	success     KnownBool
}

func decisionComparable(decision DecisionEvidence) bool {
	return decision.Status == StatusSelected || decision.Status == StatusNoSelection
}

func sameSelection(a, b DecisionEvidence) bool {
	return a.Status == b.Status && (a.Status != StatusSelected || a.SelectedCandidate == b.SelectedCandidate)
}

func shouldFallback(comparison RouteComparison, threshold float64) bool {
	decision := comparison.Shadow
	return comparison.Risk == "high" || decision.Status != StatusSelected || !decision.Confidence.Known || decision.Confidence.Value < threshold
}

func appendTelemetry(telemetry Telemetry, latencies, costs *[]float64) {
	if telemetry.Latency.Known {
		*latencies = append(*latencies, float64(telemetry.Latency.Millis))
	}
	if telemetry.CostUSD.Known {
		*costs = append(*costs, telemetry.CostUSD.Value)
	}
}

func sampleMetrics(samples []decisionSample) (Rate, Calibration) {
	successes := 0
	var brier float64
	type bin struct {
		count                int
		confidence, outcomes float64
	}
	bins := make([]bin, 10)
	calibrationSamples := 0
	for _, sample := range samples {
		if sample.success.Value {
			successes++
		}
		estimate := sample.probability
		if !estimate.Known {
			estimate = sample.confidence
		}
		if !estimate.Known {
			continue
		}
		outcome := 0.0
		if sample.success.Value {
			outcome = 1
		}
		delta := estimate.Value - outcome
		brier += delta * delta
		index := int(estimate.Value * 10)
		if index == 10 {
			index = 9
		}
		bins[index].count++
		bins[index].confidence += estimate.Value
		bins[index].outcomes += outcome
		calibrationSamples++
	}
	calibration := Calibration{Samples: calibrationSamples}
	if calibrationSamples > 0 {
		calibration.BrierScore = brier / float64(calibrationSamples)
		for _, b := range bins {
			if b.count == 0 {
				continue
			}
			weight := float64(b.count) / float64(calibrationSamples)
			calibration.ECE += weight * math.Abs(b.confidence/float64(b.count)-b.outcomes/float64(b.count))
		}
	}
	return rate(successes, len(samples)), calibration
}

func rate(count, total int) Rate {
	r := Rate{Count: count, Total: total}
	if total > 0 {
		r.Value = float64(count) / float64(total)
	}
	return r
}

func distribution(values []float64) Distribution {
	d := Distribution{Known: len(values)}
	if len(values) == 0 {
		return d
	}
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	var sum float64
	for _, value := range ordered {
		sum += value
	}
	d.Average = sum / float64(len(ordered))
	index := int(math.Ceil(0.95*float64(len(ordered)))) - 1
	d.P95 = ordered[index]
	return d
}
