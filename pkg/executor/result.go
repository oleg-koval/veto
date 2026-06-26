// Package executor defines the result type returned by model CLI executors.
package executor

// Result holds the output of a single model invocation.
type Result struct {
	Output string
	Error  error
}
