package modules

import (
	"context"
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
		{
			name:       "JIRA ID with dash separator after ID",
			branchName: "feature/TEST-999-component",
			expected:   "TEST-999",
		},
		{
			name:       "JIRA ID ending with slash",
			branchName: "release/CORE-123",
			expected:   "CORE-123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			module := NewJIRATaskDetector(JiraTransformTypeSuffix)
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
		transformType JiraTransformType
		commitMessage string
		jiraID        string
		expected      string
	}{
		// Suffix transform tests
		{
			name:          "suffix: simple single line message",
			transformType: JiraTransformTypeSuffix,
			commitMessage: "add user authentication",
			jiraID:        "PROJ-123",
			expected:      "add user authentication (PROJ-123)",
		},
		{
			name:          "suffix: no JIRA ID to add",
			transformType: JiraTransformTypeSuffix,
			commitMessage: "fix login bug",
			jiraID:        "",
			expected:      "fix login bug",
		},
		{
			name:          "suffix: message already has JIRA ID in brackets",
			transformType: JiraTransformTypeSuffix,
			commitMessage: "feat: implement OAuth (PROJ-123)",
			jiraID:        "PROJ-123",
			expected:      "feat: implement OAuth (PROJ-123)",
		},
		{
			name:          "suffix: empty message with JIRA ID",
			transformType: JiraTransformTypeSuffix,
			commitMessage: "",
			jiraID:        "ABC-456",
			expected:      " (ABC-456)",
		},
		{
			name:          "suffix: empty message no JIRA ID",
			transformType: JiraTransformTypeSuffix,
			commitMessage: "",
			jiraID:        "",
			expected:      "",
		},
		{
			name:          "suffix: conventional commit with JIRA ID",
			transformType: JiraTransformTypeSuffix,
			commitMessage: "refactor: update dependencies",
			jiraID:        "TASK-322",
			expected:      "refactor: update dependencies (TASK-322)",
		},
		{
			name:          "suffix: conventional commit feat with JIRA ID",
			transformType: JiraTransformTypeSuffix,
			commitMessage: "feat: add new feature",
			jiraID:        "PROJ-456",
			expected:      "feat: add new feature (PROJ-456)",
		},
		{
			name:          "suffix: conventional commit fix with JIRA ID",
			transformType: JiraTransformTypeSuffix,
			commitMessage: "fix: resolve bug in login",
			jiraID:        "BUG-789",
			expected:      "fix: resolve bug in login (BUG-789)",
		},
		{
			name:          "suffix: conventional commit with scope",
			transformType: JiraTransformTypeSuffix,
			commitMessage: "feat(api): added new endpoint",
			jiraID:        "TASK-123",
			expected:      "feat(api): added new endpoint (TASK-123)",
		},
		{
			name:          "suffix: multi-line commit message",
			transformType: JiraTransformTypeSuffix,
			commitMessage: "feat: add new feature\n\nThis is a detailed description\nof the new feature",
			jiraID:        "PROJ-456",
			expected:      "feat: add new feature (PROJ-456)\n\nThis is a detailed description\nof the new feature",
		},
		{
			name:          "suffix: multi-line with existing JIRA ID",
			transformType: JiraTransformTypeSuffix,
			commitMessage: "fix: bug fix (TASK-789)\n\nDetailed fix description",
			jiraID:        "TASK-789",
			expected:      "fix: bug fix (TASK-789)\n\nDetailed fix description",
		},
		{
			name:          "suffix: single line ending with punctuation",
			transformType: JiraTransformTypeSuffix,
			commitMessage: "docs: update readme.",
			jiraID:        "DOC-999",
			expected:      "docs: update readme. (DOC-999)",
		},
		// Prefix transform tests
		{
			name:          "prefix: simple single line message",
			transformType: JiraTransformTypePrefix,
			commitMessage: "add user authentication",
			jiraID:        "PROJ-123",
			expected:      "PROJ-123: add user authentication",
		},
		{
			name:          "prefix: no JIRA ID to add",
			transformType: JiraTransformTypePrefix,
			commitMessage: "fix login bug",
			jiraID:        "",
			expected:      "fix login bug",
		},
		{
			name:          "prefix: message already has JIRA ID as prefix",
			transformType: JiraTransformTypePrefix,
			commitMessage: "PROJ-123: implement OAuth",
			jiraID:        "PROJ-123",
			expected:      "PROJ-123: implement OAuth",
		},
		{
			name:          "prefix: empty message with JIRA ID",
			transformType: JiraTransformTypePrefix,
			commitMessage: "",
			jiraID:        "ABC-456",
			expected:      "ABC-456: ",
		},
		{
			name:          "prefix: conventional commit with JIRA ID",
			transformType: JiraTransformTypePrefix,
			commitMessage: "refactor: update dependencies",
			jiraID:        "TASK-322",
			expected:      "TASK-322: refactor: update dependencies",
		},
		{
			name:          "prefix: multi-line commit message",
			transformType: JiraTransformTypePrefix,
			commitMessage: "feat: add new feature\n\nThis is a detailed description\nof the new feature",
			jiraID:        "PROJ-456",
			expected:      "PROJ-456: feat: add new feature\n\nThis is a detailed description\nof the new feature",
		},
		{
			name:          "prefix: message with JIRA ID in brackets should add prefix",
			transformType: JiraTransformTypePrefix,
			commitMessage: "implement OAuth (PROJ-123)",
			jiraID:        "PROJ-123",
			expected:      "implement OAuth (PROJ-123)",
		},
		// None transform tests
		{
			name:          "none: should not modify message",
			transformType: JiraTransformTypeNone,
			commitMessage: "add user authentication",
			jiraID:        "PROJ-123",
			expected:      "add user authentication",
		},
		{
			name:          "none: should not modify message even with JIRA",
			transformType: JiraTransformTypeNone,
			commitMessage: "fix: resolve bug",
			jiraID:        "BUG-999",
			expected:      "fix: resolve bug",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			module := NewJIRATaskDetector(tt.transformType)
			result := module.addJiraID(tt.commitMessage, tt.jiraID)
			if result != tt.expected {
				t.Errorf("addJiraID(%q, %q) = %q, want %q", tt.commitMessage, tt.jiraID, result, tt.expected)
			}
		})
	}
}

func TestTransformCommitMessage(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name            string
		transformType   JiraTransformType
		branch          string
		message         string
		expectedMessage string
		expectedChanged bool
		expectedError   bool
	}{
		{
			name:            "suffix: should add JIRA ID from branch",
			transformType:   JiraTransformTypeSuffix,
			branch:          "feature/PROJ-123-new-feature",
			message:         "add new feature",
			expectedMessage: "add new feature (PROJ-123)",
			expectedChanged: true,
			expectedError:   false,
		},
		{
			name:            "prefix: should add JIRA ID from branch as prefix",
			transformType:   JiraTransformTypePrefix,
			branch:          "feature/PROJ-456-another-feature",
			message:         "implement feature",
			expectedMessage: "PROJ-456: implement feature",
			expectedChanged: true,
			expectedError:   false,
		},
		{
			name:            "none: should not modify message",
			transformType:   JiraTransformTypeNone,
			branch:          "feature/PROJ-789-feature",
			message:         "add feature",
			expectedMessage: "add feature",
			expectedChanged: false,
			expectedError:   false,
		},
		{
			name:            "suffix: no JIRA in branch",
			transformType:   JiraTransformTypeSuffix,
			branch:          "feature/new-feature",
			message:         "add feature",
			expectedMessage: "add feature",
			expectedChanged: false,
			expectedError:   false,
		},
		{
			name:            "suffix: JIRA already in message",
			transformType:   JiraTransformTypeSuffix,
			branch:          "feature/TASK-111-task",
			message:         "fix: bug (TASK-111)",
			expectedMessage: "fix: bug (TASK-111)",
			expectedChanged: true,
			expectedError:   false,
		},
		{
			name:            "prefix: JIRA already as prefix in message",
			transformType:   JiraTransformTypePrefix,
			branch:          "TASK-222",
			message:         "TASK-222: implement feature",
			expectedMessage: "TASK-222: implement feature",
			expectedChanged: true,
			expectedError:   false,
		},
		{
			name:            "suffix: multi-line message",
			transformType:   JiraTransformTypeSuffix,
			branch:          "bugfix/BUG-333-critical",
			message:         "fix: critical bug\n\nThis fixes the issue where...",
			expectedMessage: "fix: critical bug (BUG-333)\n\nThis fixes the issue where...",
			expectedChanged: true,
			expectedError:   false,
		},
		{
			name:            "prefix: multi-line message",
			transformType:   JiraTransformTypePrefix,
			branch:          "hotfix/HOT-444-urgent",
			message:         "hotfix: urgent fix\n\nThis addresses...",
			expectedMessage: "HOT-444: hotfix: urgent fix\n\nThis addresses...",
			expectedChanged: true,
			expectedError:   false,
		},
		{
			name:            "suffix: direct JIRA branch name",
			transformType:   JiraTransformTypeSuffix,
			branch:          "CORE-555",
			message:         "core update",
			expectedMessage: "core update (CORE-555)",
			expectedChanged: true,
			expectedError:   false,
		},
		{
			name:            "suffix: empty branch",
			transformType:   JiraTransformTypeSuffix,
			branch:          "",
			message:         "some message",
			expectedMessage: "some message",
			expectedChanged: false,
			expectedError:   false,
		},
		{
			name:            "suffix: empty message",
			transformType:   JiraTransformTypeSuffix,
			branch:          "feature/TEST-666-test",
			message:         "",
			expectedMessage: " (TEST-666)",
			expectedChanged: true,
			expectedError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			module := NewJIRATaskDetector(tt.transformType)
			result, changed, err := module.TransformCommitMessage(ctx, tt.branch, tt.message)

			if (err != nil) != tt.expectedError {
				t.Errorf("TransformCommitMessage() error = %v, expectedError %v", err, tt.expectedError)
				return
			}

			if result != tt.expectedMessage {
				t.Errorf("TransformCommitMessage() message = %q, want %q", result, tt.expectedMessage)
			}

			if changed != tt.expectedChanged {
				t.Errorf("TransformCommitMessage() changed = %v, want %v", changed, tt.expectedChanged)
			}
		})
	}
}

func TestName(t *testing.T) {
	module := NewJIRATaskDetector(JiraTransformTypeSuffix)
	expected := "jira_task_detector"
	if name := module.Name(); name != expected {
		t.Errorf("Name() = %q, want %q", name, expected)
	}
}

func TestTransformPrompt(t *testing.T) {
	ctx := context.Background()
	module := NewJIRATaskDetector(JiraTransformTypeSuffix)

	tests := []struct {
		name            string
		prompt          string
		expectedPrompt  string
		expectedChanged bool
		expectedError   bool
	}{
		{
			name:            "should not modify prompt",
			prompt:          "Generate a commit message for this change",
			expectedPrompt:  "Generate a commit message for this change",
			expectedChanged: false,
			expectedError:   false,
		},
		{
			name:            "empty prompt",
			prompt:          "",
			expectedPrompt:  "",
			expectedChanged: false,
			expectedError:   false,
		},
		{
			name:            "prompt with special characters",
			prompt:          "Generate message for PROJ-123 changes",
			expectedPrompt:  "Generate message for PROJ-123 changes",
			expectedChanged: false,
			expectedError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, changed, err := module.TransformPrompt(ctx, tt.prompt)

			if (err != nil) != tt.expectedError {
				t.Errorf("TransformPrompt() error = %v, expectedError %v", err, tt.expectedError)
				return
			}

			if result != tt.expectedPrompt {
				t.Errorf("TransformPrompt() prompt = %q, want %q", result, tt.expectedPrompt)
			}

			if changed != tt.expectedChanged {
				t.Errorf("TransformPrompt() changed = %v, want %v", changed, tt.expectedChanged)
			}
		})
	}
}
