package commit

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/hasansino/commit/pkg/commit/models"
)

type gitOperations struct {
	gitPath       string
	repoPath      string
	activeSession *models.StagingSessionState
	sessionLease  sync.Mutex
}

// semVer remains the small, convenient representation used by parseSemVer.
// IncrementVersion uses arbitrary precision so a valid tag cannot overflow an
// architecture-sized int.
type semVer struct {
	Major int
	Minor int
	Patch int
}

func newGitOperations(repoPath string) (*gitOperations, error) {
	return newGitOperationsContext(context.Background(), repoPath)
}

func newGitOperationsContext(ctx context.Context, repoPath string) (*gitOperations, error) {
	if err := validateGitRoutingEnvironment(); err != nil {
		return nil, err
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("native Git executable was not found: %w", err)
	}
	if err := validateGitVersion(ctx, gitPath); err != nil {
		return nil, err
	}

	absRepoPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve repository path: %w", err)
	}
	discovery := &gitOperations{gitPath: gitPath, repoPath: absRepoPath}
	inside, err := discovery.runGit(ctx, "", nil, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return nil, fmt.Errorf("failed to open Git worktree at %q: %w", absRepoPath, err)
	}
	if strings.TrimSpace(string(inside)) != "true" {
		return nil, fmt.Errorf("path %q is not inside a Git worktree", absRepoPath)
	}

	rootOutput, err := discovery.runGit(ctx, "", nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("failed to resolve Git worktree root: %w", err)
	}
	worktreeRoot := strings.TrimSpace(string(rootOutput))
	if worktreeRoot == "" {
		return nil, fmt.Errorf("git returned an empty worktree root")
	}
	if !filepath.IsAbs(worktreeRoot) {
		worktreeRoot = filepath.Join(absRepoPath, worktreeRoot)
	}
	worktreeRoot, err = filepath.Abs(worktreeRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize Git worktree root: %w", err)
	}
	discovery.repoPath = worktreeRoot
	refStorage, err := discovery.runGit(ctx, "", nil, "config", "--get", "extensions.refStorage")
	if err != nil && !gitCommandExitedWith(err, 1) {
		return nil, fmt.Errorf("failed to determine Git reference storage: %w", err)
	}
	if strings.EqualFold(strings.TrimSpace(string(refStorage)), "reftable") {
		return nil, fmt.Errorf(
			"reftable repositories are not supported because safe commit finalization requires native reference transactions",
		)
	}

	return &gitOperations{
		gitPath:  gitPath,
		repoPath: worktreeRoot,
	}, nil
}

var gitRoutingEnvironment = map[string]struct{}{
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": {},
	"GIT_COMMON_DIR":                   {},
	"GIT_DIR":                          {},
	"GIT_INDEX_FILE":                   {},
	"GIT_NAMESPACE":                    {},
	"GIT_OBJECT_DIRECTORY":             {},
	"GIT_WORK_TREE":                    {},
}

func validateGitRoutingEnvironment() error {
	for variable := range gitRoutingEnvironment {
		if value, ok := os.LookupEnv(variable); ok && value != "" {
			return fmt.Errorf("%s is not supported; unset it before running commit", variable)
		}
	}
	return nil
}

func sanitizedGitEnvironment(environment []string) []string {
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if _, routed := gitRoutingEnvironment[strings.ToUpper(name)]; !routed {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func (g *gitOperations) GetCurrentBranch(ctx context.Context) (string, error) {
	head, err := g.snapshotHead(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get HEAD: %w", err)
	}
	if head.Name == "HEAD" {
		return "HEAD", nil
	}
	return strings.TrimPrefix(head.Name, "refs/heads/"), nil
}

func (g *gitOperations) IsGitRepository(ctx context.Context) bool {
	output, err := g.runGit(ctx, "", nil, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(string(output)) == "true"
}

func (g *gitOperations) Push(ctx context.Context) (string, error) {
	return g.pushNative(ctx)
}

func (g *gitOperations) GetLatestTag(ctx context.Context) (string, error) {
	return g.getLatestTagNative(ctx)
}

func (g *gitOperations) CreateTag(ctx context.Context, tag, message string) error {
	return g.createTagNative(ctx, tag, message)
}

func (g *gitOperations) PushTag(ctx context.Context, tag string) error {
	return g.pushTagNative(ctx, tag)
}

func parseSemVer(version string) semVer {
	version = strings.TrimPrefix(version, "v")
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return semVer{}
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	patch, patchErr := strconv.Atoi(parts[2])
	if majorErr != nil || minorErr != nil || patchErr != nil {
		return semVer{}
	}
	return semVer{Major: major, Minor: minor, Patch: patch}
}

func (g *gitOperations) IncrementVersion(currentTag, incrementType string) (string, error) {
	parts := [3]*big.Int{new(big.Int), new(big.Int), new(big.Int)}
	if currentTag != "" {
		tag, ok := parseNativeSemverTag(currentTag)
		if !ok {
			return "", fmt.Errorf("invalid semantic version tag %q", currentTag)
		}
		for index := range parts {
			if _, ok := parts[index].SetString(tag.parts[index], 10); !ok {
				return "", fmt.Errorf("invalid semantic version tag %q", currentTag)
			}
		}
	}

	one := big.NewInt(1)
	switch strings.ToLower(incrementType) {
	case "major":
		parts[0].Add(parts[0], one)
		parts[1].SetInt64(0)
		parts[2].SetInt64(0)
	case "minor":
		parts[1].Add(parts[1], one)
		parts[2].SetInt64(0)
	case "patch":
		parts[2].Add(parts[2], one)
	default:
		return "", fmt.Errorf(
			"invalid increment type: %s (must be major, minor, or patch)",
			incrementType,
		)
	}
	return fmt.Sprintf("v%s.%s.%s", parts[0], parts[1], parts[2]), nil
}
