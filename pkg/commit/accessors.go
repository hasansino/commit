package commit

import (
	"context"
	"time"

	"github.com/hasansino/commit/pkg/commit/models"
)

//go:generate mockgen -source $GOFILE -package mocks -destination mocks/mocks.go

type providerAccessor interface {
	Name() string
	IsAvailable() bool
	Ask(ctx context.Context, prompt string) ([]string, error)
	SetTimeout(timeout time.Duration)
	IsLocal() bool
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
		multiLine bool,
	) (map[string]string, error)
}

type gitOperationsAccessor interface {
	IsGitRepository(ctx context.Context) bool
	GetRepoState(ctx context.Context) (string, error)
	HasConflicts(ctx context.Context) (bool, []string, error)
	BeginStaging(
		ctx context.Context,
		excludePatterns, includePatterns []string,
		useGlobalGitignore bool,
	) (*models.StagingSessionState, error)
	FinishStaging(session *models.StagingSessionState) error
	GetStagedDiff(ctx context.Context, session *models.StagingSessionState, maxSizeBytes int) (string, error)
	GetCurrentBranch(ctx context.Context) (string, error)
	CreateCommit(
		ctx context.Context,
		session *models.StagingSessionState,
		message string,
	) (models.CommitResult, error)
	Push(ctx context.Context) (string, error)
	GetLatestTag(ctx context.Context) (string, error)
	IncrementVersion(currentTag, incrementType string) (string, error)
	CreateTag(ctx context.Context, tag, message string) error
	PushTag(ctx context.Context, tag string) error
}

var _ gitOperationsAccessor = (*gitOperations)(nil)
