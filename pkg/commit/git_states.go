package commit

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
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

// readMergeHeads returns the commits recorded in MERGE_HEAD. Entries are peeled
// the way native git commit peels them into merge parents, so a merged
// annotated tag resolves to its commit.
func (g *gitOperations) readMergeHeads(ctx context.Context) ([]string, error) {
	path, err := g.resolveGitPath(ctx, "MERGE_HEAD")
	if err != nil {
		return nil, err
	}
	// #nosec G304 -- Git resolves this worktree's MERGE_HEAD path.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read MERGE_HEAD: %w", err)
	}

	heads := make([]string, 0, 1)
	for line := range strings.SplitSeq(string(data), "\n") {
		entry := strings.TrimSpace(line)
		if entry == "" {
			continue
		}
		// Only full object IDs are accepted, so an entry cannot be parsed as a
		// rev-parse option or an arbitrary revision expression.
		if len(entry) != 40 && len(entry) != 64 ||
			strings.Trim(entry, "0123456789abcdefABCDEF") != "" {
			return nil, fmt.Errorf("MERGE_HEAD contains an invalid object ID %q", entry)
		}
		output, err := g.runGit(ctx, "", nil, "rev-parse", "--verify", "--quiet", entry+"^{commit}")
		if err != nil {
			return nil, fmt.Errorf("MERGE_HEAD entry %s does not name a commit: %w", entry, err)
		}
		heads = append(heads, strings.TrimSpace(string(output)))
	}
	if len(heads) == 0 {
		return nil, fmt.Errorf("MERGE_HEAD does not name any commits")
	}
	return heads, nil
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
	for part := range bytes.SplitSeq(output, []byte{0}) {
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
