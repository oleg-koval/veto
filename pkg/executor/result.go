// Package executor defines the result type returned by model CLI executors.
package executor

// Result holds the output of a single model invocation.
type Result struct {
	Output string
	Error  error
}

// ToolProvider is implemented by executors that can actually invoke tools
// (bash, read, write, edit) during execution. HTTP-only executors are
// text-only and do not implement this — the model receives no tool definitions
// and can only return text.
type ToolProvider interface {
	EffectiveTools() []string
}
