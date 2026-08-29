package commit

import (
	"bytes"
	"context"
	"fmt"
	"os"
)

const (
	RepoStateNormal        = "normal"
	RepoStateMerging       = "merging"
	RepoStateRebasing      = "rebasing"
	RepoStateCherryPicking = "cherry-picking"
	RepoStateReverting     = "reverting"
	RepoStateBisecting     = "bisecting"
)

var repoStateMarkers = []struct {
	state string
	paths []string
}{
	{state: RepoStateRebasing, paths: []string{"rebase-merge", "rebase-apply"}},
	{state: RepoStateMerging, paths: []string{"MERGE_HEAD"}},
	{state: RepoStateCherryPicking, paths: []string{"CHERRY_PICK_HEAD"}},
	{state: RepoStateReverting, paths: []string{"REVERT_HEAD"}},
	{state: RepoStateBisecting, paths: []string{"BISECT_LOG"}},
}

// GetRepoState determines the current operation state of this worktree.
func (g *gitOperations) GetRepoState(ctx context.Context) (string, error) {
	for _, marker := range repoStateMarkers {
		for _, name := range marker.paths {
			path, err := g.resolveGitPath(ctx, name)
			if err != nil {
				return RepoStateNormal, fmt.Errorf(
					"failed to resolve repository state marker %q: %w",
					name,
					err,
				)
			}

			_, err = os.Stat(path)
			switch {
			case err == nil:
				return marker.state, nil
			case os.IsNotExist(err):
				continue
			default:
				return RepoStateNormal, fmt.Errorf(
					"failed to inspect repository state marker %q: %w",
					name,
					err,
				)
			}
		}
	}

	return RepoStateNormal, nil
}

// HasConflicts reports unresolved index entries for this worktree.
func (g *gitOperations) HasConflicts(ctx context.Context) (bool, []string, error) {
	files, err := g.GetConflictedFiles(ctx)
	if err != nil {
		return false, nil, err
	}
	return len(files) > 0, files, nil
}

// GetConflictedFiles returns each unresolved path once. Git's NUL-delimited
// output preserves filenames containing newlines, tabs, and leading spaces.
func (g *gitOperations) GetConflictedFiles(ctx context.Context) ([]string, error) {
	output, err := g.runGit(
		ctx,
		"",
		nil,
		"diff",
		"--name-only",
		"-z",
		"--diff-filter=U",
		"--",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get conflicted files: %w", err)
	}

	seen := make(map[string]struct{})
	files := make([]string, 0)
	for _, part := range bytes.Split(output, []byte{0}) {
		if len(part) == 0 {
			continue
		}
		file := string(part)
		if _, exists := seen[file]; exists {
			continue
		}
		seen[file] = struct{}{}
		files = append(files, file)
	}

	return files, nil
}
