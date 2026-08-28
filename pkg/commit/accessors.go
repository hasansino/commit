package commit

import (
	"context"
	"time"
)

//go:generate mockgen -source $GOFILE -package mocks -destination mocks/mocks.go

type providerAccessor interface {
	Name() string
	IsAvailable() bool
	Ask(ctx context.Context, prompt string) ([]string, error)
	SetTimeout(timeout time.Duration)
}

type moduleAccessor interface {
	Name() string
	TransformPrompt(ctx context.Context, prompt string) (string, bool, error)
	TransformCommitMessage(ctx context.Context, branch, message string) (string, bool, error)
}

type aiServiceAccessor interface {
	NumProviders() int
	GenerateCommitMessages(
		ctx context.Context,
		diff, branch string, files []string,
		providers []string, customPrompt string,
		first bool, multiLine bool,
	) (map[string]string, error)
}
