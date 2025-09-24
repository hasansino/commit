package commit

import "context"

//go:generate mockgen -source $GOFILE -package mocks -destination mocks/mocks.go

type providerAccessor interface {
	Name() string
	IsAvailable() bool
	Ask(ctx context.Context, prompt string) ([]string, error)
}

type moduleAccessor interface {
	Name() string
	TransformPrompt(ctx context.Context, prompt string) (string, bool, error)
	TransformCommitMessage(ctx context.Context, branch, message string) (string, bool, error)
}

type gitOperationsAccessor interface {
	IsGitRepository() bool
	GetRepoState() (string, error)
	HasConflicts() (bool, []string, error)
	GetConflictedFiles() ([]string, error)
	UnstageAll() error
	StageFiles(excludePatterns, includePatterns []string, useGlobalGitignore bool) ([]string, error)
	GetStagedDiff(maxSizeBytes int) (string, error)
	GetCurrentBranch() (string, error)
	CreateCommit(message string) error
	Push() (string, error)
	GetLatestTag() (string, error)
	IncrementVersion(currentTag, incrementType string) (string, error)
	CreateTag(tag, message string) error
	PushTag(tag string) error
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
