package commit

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGetRepoState_MainAndLinkedWorktrees(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name   string
		marker string
		state  string
		isDir  bool
	}{
		{name: "normal", state: RepoStateNormal},
		{name: "merge", marker: "MERGE_HEAD", state: RepoStateMerging},
		{name: "rebase merge", marker: "rebase-merge", state: RepoStateRebasing, isDir: true},
		{name: "rebase apply", marker: "rebase-apply", state: RepoStateRebasing, isDir: true},
		{name: "cherry-pick", marker: "CHERRY_PICK_HEAD", state: RepoStateCherryPicking},
		{name: "revert", marker: "REVERT_HEAD", state: RepoStateReverting},
		{name: "bisect", marker: "BISECT_LOG", state: RepoStateBisecting},
	}

	for _, location := range []string{"main", "linked"} {
		t.Run(location, func(t *testing.T) {
			mainPath, linkedPath := newGitStateWorktrees(t)
			mainOps := newGitStateOperations(t, mainPath)
			linkedOps := newGitStateOperations(t, linkedPath)

			selectedOps, otherOps := mainOps, linkedOps
			if location == "linked" {
				selectedOps, otherOps = linkedOps, mainOps
			}

			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					if tt.marker != "" {
						markerPath, err := selectedOps.resolveGitPath(ctx, tt.marker)
						if err != nil {
							t.Fatalf("resolveGitPath(%q) error = %v", tt.marker, err)
						}
						createGitStateMarker(t, markerPath, tt.isDir)
						t.Cleanup(func() { _ = os.RemoveAll(markerPath) })
					}

					state, err := selectedOps.GetRepoState(ctx)
					if err != nil {
						t.Fatalf("GetRepoState() error = %v", err)
					}
					if state != tt.state {
						t.Fatalf("GetRepoState() = %q, want %q", state, tt.state)
					}

					otherState, err := otherOps.GetRepoState(ctx)
					if err != nil {
						t.Fatalf("other worktree GetRepoState() error = %v", err)
					}
					if otherState != RepoStateNormal {
						t.Fatalf(
							"other worktree GetRepoState() = %q, want %q",
							otherState,
							RepoStateNormal,
						)
					}

					if tt.marker != "" {
						markerPath, err := selectedOps.resolveGitPath(ctx, tt.marker)
						if err != nil {
							t.Fatalf("resolveGitPath(%q) for cleanup error = %v", tt.marker, err)
						}
						if err := os.RemoveAll(markerPath); err != nil {
							t.Fatalf("remove marker %q: %v", markerPath, err)
						}
					}
				})
			}
		})
	}
}

func TestGitConflicts_MainAndLinkedWorktrees(t *testing.T) {
	ctx := context.Background()
	filename := "conflict file.txt"
	if runtime.GOOS != "windows" {
		filename = "conflict\nname\t-leading.txt"
	}

	for _, location := range []string{"main", "linked"} {
		t.Run(location, func(t *testing.T) {
			mainPath, targetPath := newGitConflictFixture(t, location == "linked", filename)
			targetOps := newGitStateOperations(t, targetPath)

			state, err := targetOps.GetRepoState(ctx)
			if err != nil {
				t.Fatalf("GetRepoState() during merge error = %v", err)
			}
			if state != RepoStateMerging {
				t.Fatalf("GetRepoState() during merge = %q, want %q", state, RepoStateMerging)
			}

			hasConflicts, files, err := targetOps.HasConflicts(ctx)
			if err != nil {
				t.Fatalf("HasConflicts() error = %v", err)
			}
			if !hasConflicts {
				t.Fatal("HasConflicts() = false, want true")
			}
			assertGitStatePaths(t, files, []string{filename})

			files, err = targetOps.GetConflictedFiles(ctx)
			if err != nil {
				t.Fatalf("GetConflictedFiles() error = %v", err)
			}
			assertGitStatePaths(t, files, []string{filename})

			if location == "linked" {
				mainOps := newGitStateOperations(t, mainPath)
				mainState, err := mainOps.GetRepoState(ctx)
				if err != nil {
					t.Fatalf("main GetRepoState() error = %v", err)
				}
				if mainState != RepoStateNormal {
					t.Fatalf("main GetRepoState() = %q, want %q", mainState, RepoStateNormal)
				}
				mainHasConflicts, mainFiles, err := mainOps.HasConflicts(ctx)
				if err != nil {
					t.Fatalf("main HasConflicts() error = %v", err)
				}
				if mainHasConflicts || len(mainFiles) != 0 {
					t.Fatalf(
						"main HasConflicts() = (%v, %q), want (false, no files)",
						mainHasConflicts,
						mainFiles,
					)
				}
			}

			writeGitStateFile(t, targetPath, filename, "resolved\n")
			runGitStateCommand(t, targetPath, "add", "--", filename)

			hasConflicts, files, err = targetOps.HasConflicts(ctx)
			if err != nil {
				t.Fatalf("HasConflicts() after resolution error = %v", err)
			}
			if hasConflicts || len(files) != 0 {
				t.Fatalf(
					"HasConflicts() after resolution = (%v, %q), want (false, no files)",
					hasConflicts,
					files,
				)
			}

			state, err = targetOps.GetRepoState(ctx)
			if err != nil {
				t.Fatalf("GetRepoState() after resolution error = %v", err)
			}
			if state != RepoStateMerging {
				t.Fatalf(
					"GetRepoState() after conflict resolution = %q, want %q",
					state,
					RepoStateMerging,
				)
			}
		})
	}
}

func TestGitStateMethods_ReturnGitErrors(t *testing.T) {
	ctx := context.Background()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("exec.LookPath(git) error = %v", err)
	}
	gitOps := &gitOperations{gitPath: gitPath, repoPath: t.TempDir()}

	if state, err := gitOps.GetRepoState(ctx); err == nil || state != RepoStateNormal {
		t.Fatalf("GetRepoState() = (%q, %v), want (%q, error)", state, err, RepoStateNormal)
	}
	if hasConflicts, files, err := gitOps.HasConflicts(ctx); err == nil || hasConflicts || files != nil {
		t.Fatalf(
			"HasConflicts() = (%v, %q, %v), want (false, nil, error)",
			hasConflicts,
			files,
			err,
		)
	}
}

func newGitStateWorktrees(t *testing.T) (string, string) {
	t.Helper()
	mainPath := t.TempDir()
	runGitStateCommand(t, mainPath, "init", "-q")
	runGitStateCommand(t, mainPath, "config", "user.name", "State Test")
	runGitStateCommand(t, mainPath, "config", "user.email", "state@example.invalid")
	writeGitStateFile(t, mainPath, "tracked.txt", "base\n")
	runGitStateCommand(t, mainPath, "add", "--", "tracked.txt")
	runGitStateCommand(t, mainPath, "commit", "-q", "-m", "initial")

	linkedPath := filepath.Join(t.TempDir(), "linked")
	runGitStateCommand(t, mainPath, "worktree", "add", "-q", "-b", "state-linked", linkedPath)
	return mainPath, linkedPath
}

func newGitConflictFixture(t *testing.T, linked bool, filename string) (string, string) {
	t.Helper()
	mainPath := t.TempDir()
	runGitStateCommand(t, mainPath, "init", "-q")
	runGitStateCommand(t, mainPath, "config", "user.name", "Conflict Test")
	runGitStateCommand(t, mainPath, "config", "user.email", "conflict@example.invalid")
	writeGitStateFile(t, mainPath, filename, "base\n")
	runGitStateCommand(t, mainPath, "add", "--", filename)
	runGitStateCommand(t, mainPath, "commit", "-q", "-m", "initial")
	baseBranch := strings.TrimSpace(string(runGitStateCommand(
		t,
		mainPath,
		"symbolic-ref",
		"--short",
		"HEAD",
	)))

	runGitStateCommand(t, mainPath, "checkout", "-q", "-b", "incoming")
	writeGitStateFile(t, mainPath, filename, "incoming\n")
	runGitStateCommand(t, mainPath, "add", "--", filename)
	runGitStateCommand(t, mainPath, "commit", "-q", "-m", "incoming")
	runGitStateCommand(t, mainPath, "checkout", "-q", baseBranch)

	targetPath := mainPath
	if linked {
		targetPath = filepath.Join(t.TempDir(), "linked")
		runGitStateCommand(
			t,
			mainPath,
			"worktree",
			"add",
			"-q",
			"-b",
			"conflict-linked",
			targetPath,
		)
	}

	writeGitStateFile(t, targetPath, filename, "ours\n")
	runGitStateCommand(t, targetPath, "add", "--", filename)
	runGitStateCommand(t, targetPath, "commit", "-q", "-m", "ours")
	runGitStateCommandFailure(t, targetPath, "merge", "--no-edit", "incoming")

	return mainPath, targetPath
}

func newGitStateOperations(t *testing.T, repoPath string) *gitOperations {
	t.Helper()
	gitOps, err := newGitOperations(repoPath)
	if err != nil {
		t.Fatalf("newGitOperations(%q) error = %v", repoPath, err)
	}
	return gitOps
}

func createGitStateMarker(t *testing.T, path string, directory bool) {
	t.Helper()
	if directory {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("create state marker directory %q: %v", path, err)
		}
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create state marker parent %q: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("state test\n"), 0o600); err != nil {
		t.Fatalf("create state marker %q: %v", path, err)
	}
}

func writeGitStateFile(t *testing.T, repoPath, name, contents string) {
	t.Helper()
	path := filepath.Join(repoPath, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create parent for %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func runGitStateCommand(t *testing.T, repoPath string, args ...string) []byte {
	t.Helper()
	output, err := gitStateCommand(repoPath, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
	return output
}

func runGitStateCommandFailure(t *testing.T, repoPath string, args ...string) []byte {
	t.Helper()
	output, err := gitStateCommand(repoPath, args...).CombinedOutput()
	if err == nil {
		t.Fatalf("git %s unexpectedly succeeded\n%s", strings.Join(args, " "), output)
	}
	return output
}

func gitStateCommand(repoPath string, args ...string) *exec.Cmd {
	commandArgs := append([]string{"-C", repoPath}, args...)
	cmd := exec.Command("git", commandArgs...)
	cmd.Env = append(
		os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"LC_ALL=C",
	)
	return cmd
}

func assertGitStatePaths(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("paths = %q, want %q", got, want)
	}
	for index := range want {
		if !bytes.Equal([]byte(got[index]), []byte(want[index])) {
			t.Fatalf("paths = %q, want %q", got, want)
		}
	}
}
