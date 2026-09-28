package router

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

// PrivateWitnessVersion is independent of the redacted shadow evidence schema.
const PrivateWitnessVersion = 1

// PrivateCandidateBinding is a route-time, private key-to-model observation.
// It must never be written to the redacted shadow evidence stream.
type PrivateCandidateBinding struct {
	Key   string `json:"key"`
	Model string `json:"model"`
}

// PrivateRouteWitness binds a route observation to its original candidate
// names and a fingerprint of the replayable task fields. A fingerprint is not
// anonymization: this entire record is private and must not be published.
type PrivateRouteWitness struct {
	Version         int                       `json:"version"`
	RouteID         string                    `json:"route_id"`
	ObservedAt      time.Time                 `json:"observed_at"`
	TaskKind        TaskKind                  `json:"task_kind"`
	Risk            Risk                      `json:"risk"`
	RouteKey        string                    `json:"route_key"`
	TaskFingerprint string                    `json:"task_fingerprint"`
	Bindings        []PrivateCandidateBinding `json:"bindings"`
}

// PrivateRouteWitnessRecorder is only called when explicitly installed on a
// shadow engine. Implementations must tolerate concurrent Decide calls. Its
// errors cannot change the authoritative routing result.
type PrivateRouteWitnessRecorder interface {
	RecordPrivateRouteWitness(PrivateRouteWitness) error
}

// PrivateCandidateKey recomputes one opaque key using the private route key.
// Only a route-time witness may supply that key; do not publish it.
func PrivateCandidateKey(routeKeyHex, modelName string) (string, error) {
	key, err := decodePrivateRouteKey(routeKeyHex)
	if err != nil || modelName == "" {
		return "", errors.New("invalid private candidate binding")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("candidate\x00" + modelName))
	return "c-" + hex.EncodeToString(mac.Sum(nil)[:16]), nil
}

// PrivateTaskFingerprint binds the replayable task fields without retaining
// their raw text in the witness. The route key and digest are private.
func PrivateTaskFingerprint(routeKeyHex string, task TaskSpec) (string, error) {
	key, err := decodePrivateRouteKey(routeKeyHex)
	if err != nil {
		return "", err
	}
	value := struct {
		Kind            TaskKind `json:"kind"`
		Risk            Risk     `json:"risk"`
		Objective       string   `json:"objective"`
		Criteria        []string `json:"criteria"`
		MaxOutputTokens int      `json:"max_output_tokens"`
	}{
		Kind: task.Kind, Risk: task.Risk, Objective: task.Objective,
		Criteria:        append([]string{}, task.SuccessCriteria...),
		MaxOutputTokens: task.ExecutionMaxOutputTokens,
	}
	encoded, _ := json.Marshal(value) // fixed fields cannot fail to marshal
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(append([]byte("task\x00"), encoded...))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func decodePrivateRouteKey(value string) ([]byte, error) {
	key, err := hex.DecodeString(value)
	if err != nil || len(key) != 32 {
		return nil, errors.New("invalid private route key")
	}
	return key, nil
}
