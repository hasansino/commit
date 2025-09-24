package commit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// GetRepoState determines the current state of the repository
func (g *gitOperations) GetRepoState() (string, error) {
	// Get the worktree path
	wt, err := g.repo.Worktree()
	if err != nil {
		return RepoStateNormal, fmt.Errorf("failed to get worktree: %w", err)
	}

	gitDir := filepath.Join(wt.Filesystem.Root(), ".git")

	// For worktrees, .git might be a file pointing to the actual git directory
	if info, err := os.Stat(gitDir); err == nil && !info.IsDir() {
		// Read the gitdir path from the file
		content, err := os.ReadFile(gitDir)
		if err != nil {
			return RepoStateNormal, fmt.Errorf("failed to read .git file: %w", err)
		}
		// Parse "gitdir: /path/to/.git/worktrees/name"
		gitDirLine := strings.TrimSpace(string(content))
		if strings.HasPrefix(gitDirLine, "gitdir: ") {
			gitDir = strings.TrimPrefix(gitDirLine, "gitdir: ")
			// Get the common dir for worktrees
			gitDir = filepath.Dir(gitDir)
			if strings.Contains(gitDir, "/worktrees/") {
				gitDir = filepath.Dir(filepath.Dir(gitDir))
			}
		}
	}

	// Check for rebase
	if fileExists(filepath.Join(gitDir, "rebase-merge")) ||
		fileExists(filepath.Join(gitDir, "rebase-apply")) {
		return RepoStateRebasing, nil
	}

	// Check for merge
	if fileExists(filepath.Join(gitDir, "MERGE_HEAD")) {
		return RepoStateMerging, nil
	}

	// Check for cherry-pick
	if fileExists(filepath.Join(gitDir, "CHERRY_PICK_HEAD")) {
		return RepoStateCherryPicking, nil
	}

	// Check for revert
	if fileExists(filepath.Join(gitDir, "REVERT_HEAD")) {
		return RepoStateReverting, nil
	}

	// Check for bisect
	if fileExists(filepath.Join(gitDir, "BISECT_LOG")) {
		return RepoStateBisecting, nil
	}

	return RepoStateNormal, nil
}

// HasConflicts checks if there are any unresolved merge conflicts
func (g *gitOperations) HasConflicts() (bool, []string, error) {
	// Get the worktree path
	wt, err := g.repo.Worktree()
	if err != nil {
		return false, nil, fmt.Errorf("failed to get worktree: %w", err)
	}

	cmd := exec.Command("git", "-C", wt.Filesystem.Root(), "diff", "--name-only", "--diff-filter=U")
	output, err := cmd.Output()
	if err != nil {
		// If the command fails, it might mean no conflicts or git error
		// Check git status to be sure
		return g.hasConflictsViaStatus()
	}

	if len(output) == 0 {
		return false, nil, nil
	}

	files := strings.Split(strings.TrimSpace(string(output)), "\n")
	// Filter empty strings
	var conflictedFiles []string
	for _, f := range files {
		if f != "" {
			conflictedFiles = append(conflictedFiles, f)
		}
	}

	return len(conflictedFiles) > 0, conflictedFiles, nil
}

// hasConflictsViaStatus is a fallback method using git status
func (g *gitOperations) hasConflictsViaStatus() (bool, []string, error) {
	files, err := g.GetConflictedFiles()
	if err != nil {
		return false, nil, err
	}
	return len(files) > 0, files, nil
}

// GetConflictedFiles returns detailed information about conflicted files
func (g *gitOperations) GetConflictedFiles() ([]string, error) {
	// Get the worktree path
	wt, err := g.repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree: %w", err)
	}

	cmd := exec.Command("git", "-C", wt.Filesystem.Root(), "status", "--porcelain")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get git status: %w", err)
	}

	var conflicted []string
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if len(line) > 2 {
			status := line[:2]
			file := strings.TrimSpace(line[3:])

			// Check for conflict markers
			// UU = both modified, AA = both added, DD = both deleted
			// AU/UA = added by us/them, DU/UD = deleted by us/them
			if isConflictStatus(status) {
				conflicted = append(conflicted, file)
			}
		}
	}

	return conflicted, nil
}

// isConflictStatus checks if a git status indicates a conflict
func isConflictStatus(status string) bool {
	conflictStatuses := []string{"UU", "AA", "DD", "AU", "UA", "DU", "UD"}
	for _, cs := range conflictStatuses {
		if status == cs {
			return true
		}
	}
	return false
}

// fileExists checks if a file exists
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
