package modules

import (
	"testing"
)

func TestDetectJiraID(t *testing.T) {
	tests := []struct {
		name       string
		branchName string
		expected   string
	}{
		{
			name:       "direct JIRA issue",
			branchName: "PROJ-123",
			expected:   "PROJ-123",
		},
		{
			name:       "feature branch with JIRA",
			branchName: "feature/PROJ-456-add-login",
			expected:   "PROJ-456",
		},
		{
			name:       "bugfix branch with JIRA",
			branchName: "bugfix/ABC-789-fix-auth",
			expected:   "ABC-789",
		},
		{
			name:       "hotfix branch with JIRA",
			branchName: "hotfix/DEF-321-critical-fix",
			expected:   "DEF-321",
		},
		{
			name:       "chore branch with JIRA",
			branchName: "chore/GHI-654-update-deps",
			expected:   "GHI-654",
		},
		{
			name:       "custom prefix with JIRA",
			branchName: "custom/JKL-999-something",
			expected:   "JKL-999",
		},
		{
			name:       "master branch",
			branchName: "master",
			expected:   "",
		},
		{
			name:       "develop branch",
			branchName: "develop",
			expected:   "",
		},
		{
			name:       "no JIRA pattern",
			branchName: "feature/some-feature",
			expected:   "",
		},
		{
			name:       "empty branch",
			branchName: "",
			expected:   "",
		},
		{
			name:       "feature branch without JIRA",
			branchName: "feature/add-new-component",
			expected:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			module := NewJIRAPrefixDetector()
			result := module.detectJiraID(tt.branchName)
			if result != tt.expected {
				t.Errorf("detectJiraID(%q) = %q, want %q", tt.branchName, result, tt.expected)
			}
		})
	}
}

func TestAddJiraID(t *testing.T) {
	tests := []struct {
		name          string
		commitMessage string
		jiraID        string
		expected      string
	}{
		{
			name:          "simple single line message",
			commitMessage: "add user authentication",
			jiraID:        "PROJ-123",
			expected:      "add user authentication (PROJ-123)",
		},
		{
			name:          "no JIRA ID to add",
			commitMessage: "fix login bug",
			jiraID:        "",
			expected:      "fix login bug",
		},
		{
			name:          "message already has JIRA ID in brackets",
			commitMessage: "feat: implement OAuth (PROJ-123)",
			jiraID:        "PROJ-123",
			expected:      "feat: implement OAuth (PROJ-123)",
		},
		{
			name:          "empty message with JIRA ID",
			commitMessage: "",
			jiraID:        "ABC-456",
			expected:      " (ABC-456)",
		},
		{
			name:          "empty message no JIRA ID",
			commitMessage: "",
			jiraID:        "",
			expected:      "",
		},
		{
			name:          "conventional commit with JIRA ID",
			commitMessage: "refactor: update dependencies",
			jiraID:        "TASK-322",
			expected:      "refactor: update dependencies (TASK-322)",
		},
		{
			name:          "conventional commit feat with JIRA ID",
			commitMessage: "feat: add new feature",
			jiraID:        "PROJ-456",
			expected:      "feat: add new feature (PROJ-456)",
		},
		{
			name:          "conventional commit fix with JIRA ID",
			commitMessage: "fix: resolve bug in login",
			jiraID:        "BUG-789",
			expected:      "fix: resolve bug in login (BUG-789)",
		},
		{
			name:          "conventional commit with scope",
			commitMessage: "feat(api): added new endpoint",
			jiraID:        "TASK-123",
			expected:      "feat(api): added new endpoint (TASK-123)",
		},
		{
			name:          "multi-line commit message",
			commitMessage: "feat: add new feature\n\nThis is a detailed description\nof the new feature",
			jiraID:        "PROJ-456",
			expected:      "feat: add new feature (PROJ-456)\n\nThis is a detailed description\nof the new feature",
		},
		{
			name:          "multi-line with existing JIRA ID",
			commitMessage: "fix: bug fix (TASK-789)\n\nDetailed fix description",
			jiraID:        "TASK-789",
			expected:      "fix: bug fix (TASK-789)\n\nDetailed fix description",
		},
		{
			name:          "single line ending with punctuation",
			commitMessage: "docs: update readme.",
			jiraID:        "DOC-999",
			expected:      "docs: update readme. (DOC-999)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			module := NewJIRAPrefixDetector()
			result := module.addJiraID(tt.commitMessage, tt.jiraID)
			if result != tt.expected {
				t.Errorf("addJiraID(%q, %q) = %q, want %q", tt.commitMessage, tt.jiraID, result, tt.expected)
			}
		})
	}
}
