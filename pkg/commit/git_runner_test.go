package commit

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestGitStateMethodsReturnCommandErrors(t *testing.T) {
	gitOps := &gitOperations{gitPath: filepath.Join(t.TempDir(), "missing-git")}
	ctx := context.Background()
	if state, err := gitOps.GetRepoState(ctx); err == nil || state != RepoStateNormal {
		t.Fatalf("GetRepoState() = (%q, %v), want normal state and a command error", state, err)
	}
	if conflicts, files, err := gitOps.HasConflicts(ctx); err == nil || conflicts || files != nil {
		t.Fatalf("HasConflicts() = (%v, %q, %v), want no files and a command error", conflicts, files, err)
	}
}

func TestGitOperationsHonorCancellationBeforeDispatch(t *testing.T) {
	gitOps := &gitOperations{gitPath: filepath.Join(t.TempDir(), "missing-git")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := gitOps.pushNative(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("pushNative() error = %v, want context.Canceled", err)
	}
	if err := gitOps.createTagNative(ctx, "v1.2.3", "message"); !errors.Is(err, context.Canceled) {
		t.Fatalf("createTagNative() error = %v, want context.Canceled", err)
	}
}

func TestParseGitVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		output    string
		wantMajor int
		wantMinor int
		wantErr   bool
	}{
		{name: "standard", output: "git version 2.50.1", wantMajor: 2, wantMinor: 50},
		{name: "apple", output: "git version 2.50.1 (Apple Git-155)", wantMajor: 2, wantMinor: 50},
		{name: "windows", output: "git version 2.47.1.windows.1", wantMajor: 2, wantMinor: 47},
		{name: "invalid", output: "unknown", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			major, minor, err := parseGitVersion(test.output)
			if (err != nil) != test.wantErr {
				t.Fatalf("parseGitVersion() error = %v, wantErr %v", err, test.wantErr)
			}
			if major != test.wantMajor || minor != test.wantMinor {
				t.Fatalf(
					"parseGitVersion() = %d.%d, want %d.%d",
					major, minor, test.wantMajor, test.wantMinor,
				)
			}
		})
	}
}

func TestSanitizedGitEnvironmentIsCaseInsensitive(t *testing.T) {
	t.Parallel()

	got := sanitizedGitEnvironment([]string{
		"PATH=/bin",
		"git_index_file=/tmp/hostile",
		"Git_Work_Tree=/tmp/other",
	})
	if len(got) != 1 || got[0] != "PATH=/bin" {
		t.Fatalf("sanitizedGitEnvironment() = %#v, want only PATH", got)
	}
}

func TestGitCommandMayUseTerminalFindsSubcommandAfterGlobalOptions(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{
			name: "commit after config",
			args: []string{"-c", "core.logAllRefUpdates=true", "commit", "--file", "message"},
			want: true,
		},
		{
			name: "add after literal pathspecs",
			args: []string{"--literal-pathspecs", "add", "--pathspec-from-file=-"},
			want: true,
		},
		{
			name: "add after config and literal pathspecs",
			args: []string{
				"-c", "core.excludesFile=/tmp/empty", "--literal-pathspecs",
				"add", "--pathspec-from-file=-", "--pathspec-file-nul",
			},
			want: true,
		},
		{
			name: "noninteractive diff after literal pathspecs",
			args: []string{"--literal-pathspecs", "diff", "--cached"},
			want: false,
		},
		{
			name: "incomplete config option",
			args: []string{"-c"},
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := gitCommandMayUseTerminal(nil, test.args); got != test.want {
				t.Fatalf("gitCommandMayUseTerminal(nil, %q) = %v, want %v", test.args, got, test.want)
			}
		})
	}
}
