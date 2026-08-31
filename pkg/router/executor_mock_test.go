package router

import (
	"context"
	"sync"
)

type executorMock struct {
	RunFunc func(context.Context, string) AdmissionResult

	mu    sync.RWMutex
	calls []struct {
		Ctx    context.Context
		Prompt string
	}
}

func (mock *executorMock) Run(ctx context.Context, prompt string) AdmissionResult {
	if mock.RunFunc == nil {
		panic("executorMock.RunFunc: method is nil but Executor.Run was called")
	}
	mock.mu.Lock()
	mock.calls = append(mock.calls, struct {
		Ctx    context.Context
		Prompt string
	}{Ctx: ctx, Prompt: prompt})
	mock.mu.Unlock()
	return mock.RunFunc(ctx, prompt)
}

func (mock *executorMock) RunCalls() []struct {
	Ctx    context.Context
	Prompt string
} {
	mock.mu.RLock()
	defer mock.mu.RUnlock()
	return append([]struct {
		Ctx    context.Context
		Prompt string
	}{}, mock.calls...)
}
