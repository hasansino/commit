package commit

// Keep this mock in package commit because the accessor deliberately passes an
// unexported, per-invocation staging session.
//go:generate mockgen -source $GOFILE -package commit -destination git_accessor_mock_test.go

type gitOperationsAccessor interface {
	IsGitRepository() bool
	GetRepoState() (string, error)
	HasConflicts() (bool, []string, error)
	GetConflictedFiles() ([]string, error)
	BeginStaging(excludePatterns, includePatterns []string, useGlobalGitignore bool) (*stagingSession, error)
	FinishStaging(session *stagingSession) error
	GetStagedDiff(maxSizeBytes int) (string, error)
	GetCurrentBranch() (string, error)
	CreateCommit(session *stagingSession, message string) error
	Push() (string, error)
	GetLatestTag() (string, error)
	IncrementVersion(currentTag, incrementType string) (string, error)
	CreateTag(tag, message string) error
	PushTag(tag string) error
}

var _ gitOperationsAccessor = (*gitOperations)(nil)
