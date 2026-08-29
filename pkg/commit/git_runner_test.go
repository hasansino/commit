package commit

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

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

func TestNewGitOperationsRejectsReftable(t *testing.T) {
	repoPath := t.TempDir()
	cmd := exec.Command("git", "-C", repoPath, "init", "-q", "--ref-format=reftable")
	cmd.Env = sanitizedGitEnvironment(os.Environ())
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("installed Git does not support reftable fixtures: %v: %s", err, output)
	}

	_, err := newGitOperations(repoPath)
	if err == nil || !strings.Contains(err.Error(), "reftable repositories are not supported") {
		t.Fatalf("newGitOperations(reftable) error = %v, want explicit rejection", err)
	}
}
