package modules

import (
	"testing"
)

func TestDetectJIRAPrefix(t *testing.T) {
	tests := []struct {
		name       string
		branchName string
		expected   string
	}{
		{
			name:       "direct JIRA issue",
			branchName: "PROJ-123",
			expected:   "PROJ-123: ",
		},
		{
			name:       "feature branch with JIRA",
			branchName: "feature/PROJ-456-add-login",
			expected:   "PROJ-456: ",
		},
		{
			name:       "bugfix branch with JIRA",
			branchName: "bugfix/ABC-789-fix-auth",
			expected:   "ABC-789: ",
		},
		{
			name:       "hotfix branch with JIRA",
			branchName: "hotfix/DEF-321-critical-fix",
			expected:   "DEF-321: ",
		},
		{
			name:       "chore branch with JIRA",
			branchName: "chore/GHI-654-update-deps",
			expected:   "GHI-654: ",
		},
		{
			name:       "custom prefix with JIRA",
			branchName: "custom/JKL-999-something",
			expected:   "JKL-999: ",
		},
		{
			name:       "main branch",
			branchName: "main",
			expected:   "",
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
			result := module.detectJIRAPrefix(tt.branchName)
			if result != tt.expected {
				t.Errorf("DetectJIRAPrefix(%q) = %q, want %q", tt.branchName, result, tt.expected)
			}
		})
	}
}

func TestApplyJIRAPrefix(t *testing.T) {
	tests := []struct {
		name          string
		commitMessage string
		jiraPrefix    string
		expected      string
	}{
		{
			name:          "apply prefix to message",
			commitMessage: "add user authentication",
			jiraPrefix:    "PROJ-123: ",
			expected:      "PROJ-123: add user authentication",
		},
		{
			name:          "no prefix to apply",
			commitMessage: "fix login bug",
			jiraPrefix:    "",
			expected:      "fix login bug",
		},
		{
			name:          "message already has prefix",
			commitMessage: "PROJ-123: implement OAuth",
			jiraPrefix:    "PROJ-123: ",
			expected:      "PROJ-123: implement OAuth",
		},
		{
			name:          "empty message with prefix",
			commitMessage: "",
			jiraPrefix:    "ABC-456: ",
			expected:      "ABC-456: ",
		},
		{
			name:          "empty message no prefix",
			commitMessage: "",
			jiraPrefix:    "",
			expected:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			module := NewJIRAPrefixDetector()
			result := module.applyJIRAPrefix(tt.commitMessage, tt.jiraPrefix)
			if result != tt.expected {
				t.Errorf("ApplyJIRAPrefix(%q, %q) = %q, want %q", tt.commitMessage, tt.jiraPrefix, result, tt.expected)
			}
		})
	}
}
