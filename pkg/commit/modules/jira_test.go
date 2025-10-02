package modules

import (
	"context"
	"testing"
)

func TestJiraCornerCases(t *testing.T) {
	tests := []struct {
		name          string
		position      JiraTaskPosition
		style         JiraTaskStyle
		branch        string
		commitMessage string
		expected      string
		shouldChange  bool
	}{
		// Non-conventional commits with colons
		{
			name:          "url in message - prefix",
			position:      JiraTaskPositionPrefix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/TASK-123-feature",
			commitMessage: "Update https://example.com link",
			expected:      "[TASK-123] Update https://example.com link",
			shouldChange:  true,
		},
		{
			name:          "url in message - infix",
			position:      JiraTaskPositionInfix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/TASK-123-feature",
			commitMessage: "Update https://example.com link",
			expected:      "[TASK-123] Update https://example.com link",
			shouldChange:  true,
		},
		{
			name:          "time format in message - infix",
			position:      JiraTaskPositionInfix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/TASK-456-feature",
			commitMessage: "Meeting at 10:30: discuss architecture",
			expected:      "[TASK-456] Meeting at 10:30: discuss architecture",
			shouldChange:  true,
		},
		{
			name:          "multiple colons - infix",
			position:      JiraTaskPositionInfix,
			style:         JiraTaskStyleParens,
			branch:        "feature/BUG-789-fix",
			commitMessage: "Fix issue: user:password format breaks",
			expected:      "(BUG-789) Fix issue: user:password format breaks",
			shouldChange:  true,
		},
		{
			name:          "colon at end - infix",
			position:      JiraTaskPositionInfix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/PROJ-111-feature",
			commitMessage: "Added the following:",
			expected:      "[PROJ-111] Added the following:",
			shouldChange:  true,
		},
		{
			name:          "windows path - infix",
			position:      JiraTaskPositionInfix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/TASK-222-feature",
			commitMessage: "Fix path C:\\Users\\test",
			expected:      "[TASK-222] Fix path C:\\Users\\test",
			shouldChange:  true,
		},
		{
			name:          "emoji with colon notation",
			position:      JiraTaskPositionInfix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/TASK-333-feature",
			commitMessage: ":sparkles: Add new feature",
			expected:      "[TASK-333] :sparkles: Add new feature",
			shouldChange:  true,
		},
		{
			name:          "ratio notation - infix",
			position:      JiraTaskPositionInfix,
			style:         JiraTaskStyleParens,
			branch:        "feature/TASK-444-feature",
			commitMessage: "Improve aspect ratio 16:9 support",
			expected:      "(TASK-444) Improve aspect ratio 16:9 support",
			shouldChange:  true,
		},

		// Messages that look like conventional commits but aren't
		{
			name:          "fake conventional - missing space after colon",
			position:      JiraTaskPositionInfix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/TASK-555-feature",
			commitMessage: "feat:missing space",
			expected:      "[TASK-555] feat:missing space",
			shouldChange:  true,
		},
		{
			name:          "fake conventional - uppercase type",
			position:      JiraTaskPositionInfix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/TASK-666-feature",
			commitMessage: "FEAT: uppercase type",
			expected:      "[TASK-666] FEAT: uppercase type",
			shouldChange:  true,
		},
		{
			name:          "fake conventional - number in type",
			position:      JiraTaskPositionInfix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/TASK-777-feature",
			commitMessage: "feat2: numbered type",
			expected:      "[TASK-777] feat2: numbered type",
			shouldChange:  true,
		},

		// Special characters and edge cases
		{
			name:          "empty message",
			position:      JiraTaskPositionPrefix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/TASK-888-feature",
			commitMessage: "",
			expected:      "[TASK-888] ",
			shouldChange:  true,
		},
		{
			name:          "only whitespace",
			position:      JiraTaskPositionSuffix,
			style:         JiraTaskStyleParens,
			branch:        "feature/TASK-999-feature",
			commitMessage: "   ",
			expected:      "    (TASK-999)",
			shouldChange:  true,
		},
		{
			name:          "message with tabs",
			position:      JiraTaskPositionPrefix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/TASK-100-feature",
			commitMessage: "Fix\ttab\tissues",
			expected:      "[TASK-100] Fix\ttab\tissues",
			shouldChange:  true,
		},
		{
			name:          "message with newline at start",
			position:      JiraTaskPositionPrefix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/TASK-101-feature",
			commitMessage: "\nStarting with newline",
			expected:      "[TASK-101] \nStarting with newline",
			shouldChange:  true,
		},
		{
			name:          "unicode characters",
			position:      JiraTaskPositionSuffix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/TASK-102-feature",
			commitMessage: "Add 中文 support 🚀",
			expected:      "Add 中文 support 🚀 [TASK-102]",
			shouldChange:  true,
		},
		{
			name:          "very long type-like prefix",
			position:      JiraTaskPositionInfix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/TASK-103-feature",
			commitMessage: "verylongtypethatlookslikeconventionalcommit: message",
			expected:      "[TASK-103] verylongtypethatlookslikeconventionalcommit: message", // Too long to be conventional commit
			shouldChange:  true,
		},
		{
			name:          "parentheses in commit message",
			position:      JiraTaskPositionPrefix,
			style:         JiraTaskStyleParens,
			branch:        "feature/TASK-104-feature",
			commitMessage: "Fix function(arg1, arg2) call",
			expected:      "(TASK-104) Fix function(arg1, arg2) call",
			shouldChange:  true,
		},
		{
			name:          "brackets in commit message",
			position:      JiraTaskPositionPrefix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/TASK-105-feature",
			commitMessage: "Update array[0] indexing",
			expected:      "[TASK-105] Update array[0] indexing",
			shouldChange:  true,
		},
		{
			name:          "JIRA-like pattern but not in branch",
			position:      JiraTaskPositionPrefix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/new-feature",
			commitMessage: "Fix PROJ-999 reference in docs",
			expected:      "Fix PROJ-999 reference in docs",
			shouldChange:  false,
		},
		{
			name:          "JIRA ID already formatted with different style",
			position:      JiraTaskPositionPrefix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/TASK-106-feature",
			commitMessage: "(TASK-106) Already has parens",
			expected:      "(TASK-106) Already has parens",
			shouldChange:  true,
		},
		{
			name:          "JIRA ID partially in message",
			position:      JiraTaskPositionPrefix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/TASK-107-feature",
			commitMessage: "Fix issue with TASK- naming",
			expected:      "[TASK-107] Fix issue with TASK- naming",
			shouldChange:  true,
		},
		{
			name:          "Multiple JIRA-like patterns",
			position:      JiraTaskPositionSuffix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/MAIN-108-feature",
			commitMessage: "Merge TASK-999 into PROJ-888",
			expected:      "Merge TASK-999 into PROJ-888 [MAIN-108]",
			shouldChange:  true,
		},

		// Valid conventional commits
		{
			name:          "valid feat - infix",
			position:      JiraTaskPositionInfix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/TASK-200-feature",
			commitMessage: "feat: add new feature",
			expected:      "feat: [TASK-200] add new feature",
			shouldChange:  true,
		},
		{
			name:          "valid fix with scope - infix",
			position:      JiraTaskPositionInfix,
			style:         JiraTaskStyleParens,
			branch:        "bugfix/BUG-201-fix",
			commitMessage: "fix(api): resolve error",
			expected:      "fix(api): (BUG-201) resolve error",
			shouldChange:  true,
		},
		{
			name:          "valid chore with scope and exclamation - infix",
			position:      JiraTaskPositionInfix,
			style:         JiraTaskStyleBrackets,
			branch:        "chore/TASK-202-update",
			commitMessage: "chore(deps)!: update dependencies",
			expected:      "chore(deps)!: [TASK-202] update dependencies",
			shouldChange:  true,
		},
		{
			name:          "only colon no space",
			position:      JiraTaskPositionInfix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/TASK-203-feature",
			commitMessage: ":",
			expected:      "[TASK-203] :",
			shouldChange:  true,
		},
		{
			name:          "colon at start",
			position:      JiraTaskPositionInfix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/TASK-204-feature",
			commitMessage: ": message",
			expected:      "[TASK-204] : message", // Not a valid conventional commit
			shouldChange:  true,
		},

		// Branch name edge cases
		{
			name:          "JIRA ID with lowercase letters",
			position:      JiraTaskPositionPrefix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/task-205-feature",
			commitMessage: "Add feature",
			expected:      "Add feature",
			shouldChange:  false,
		},
		{
			name:          "JIRA ID without number",
			position:      JiraTaskPositionPrefix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/TASK-feature",
			commitMessage: "Add feature",
			expected:      "Add feature",
			shouldChange:  false,
		},
		{
			name:          "Multiple valid JIRA patterns in branch",
			position:      JiraTaskPositionPrefix,
			style:         JiraTaskStyleBrackets,
			branch:        "TASK-300/PROJ-400",
			commitMessage: "Add feature",
			expected:      "[TASK-300] Add feature",
			shouldChange:  true,
		},

		// Style none (plain) with special cases
		{
			name:          "plain style with brackets in message",
			position:      JiraTaskPositionPrefix,
			style:         JiraTaskStylePlain,
			branch:        "feature/TASK-500-feature",
			commitMessage: "Fix [important] issue",
			expected:      "TASK-500 Fix [important] issue",
			shouldChange:  true,
		},
		{
			name:          "plain style with existing formatted ID",
			position:      JiraTaskPositionPrefix,
			style:         JiraTaskStylePlain,
			branch:        "feature/TASK-501-feature",
			commitMessage: "[TASK-501] Already formatted",
			expected:      "[TASK-501] Already formatted",
			shouldChange:  true,
		},
	}

	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector := NewJIRATaskDetector(tt.position, tt.style)
			result, changed, err := detector.TransformCommitMessage(ctx, tt.branch, tt.commitMessage)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if changed != tt.shouldChange {
				t.Errorf("expected changed=%v, got %v", tt.shouldChange, changed)
			}

			if result != tt.expected {
				t.Errorf("expected message %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestJiraMultilineMessages(t *testing.T) {
	tests := []struct {
		name          string
		position      JiraTaskPosition
		style         JiraTaskStyle
		branch        string
		commitMessage string
		expected      string
	}{
		{
			name:     "multiline with conventional commit",
			position: JiraTaskPositionInfix,
			style:    JiraTaskStyleBrackets,
			branch:   "feature/TASK-600-feature",
			commitMessage: `feat: add new feature

This is a detailed description
of the new feature that spans
multiple lines`,
			expected: `feat: [TASK-600] add new feature

This is a detailed description
of the new feature that spans
multiple lines`,
		},
		{
			name:     "multiline with URL containing colon",
			position: JiraTaskPositionInfix,
			style:    JiraTaskStyleParens,
			branch:   "feature/TASK-601-feature",
			commitMessage: `Update documentation: see https://example.com

More details here
And here`,
			expected: `(TASK-601) Update documentation: see https://example.com

More details here
And here`,
		},
		{
			name:     "multiline with colon in body",
			position: JiraTaskPositionPrefix,
			style:    JiraTaskStyleBrackets,
			branch:   "feature/TASK-602-feature",
			commitMessage: `Simple first line

Second line with: colon
Third line`,
			expected: `[TASK-602] Simple first line

Second line with: colon
Third line`,
		},
		{
			name:     "multiline with empty first line",
			position: JiraTaskPositionSuffix,
			style:    JiraTaskStyleBrackets,
			branch:   "feature/TASK-603-feature",
			commitMessage: `

Body starts here`,
			expected: ` [TASK-603]

Body starts here`,
		},
	}

	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector := NewJIRATaskDetector(tt.position, tt.style)
			result, _, err := detector.TransformCommitMessage(ctx, tt.branch, tt.commitMessage)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result != tt.expected {
				t.Errorf("expected message:\n%q\ngot:\n%q", tt.expected, result)
			}
		})
	}
}

func TestJiraIDDetection(t *testing.T) {
	detector := NewJIRATaskDetector(JiraTaskPositionPrefix, JiraTaskStyleBrackets)

	tests := []struct {
		branch   string
		expected string
	}{
		{"feature/PROJ-123-new-feature", "PROJ-123"},
		{"bugfix/BUG-456-fix", "BUG-456"},
		{"hotfix/HOT-789-urgent", "HOT-789"},
		{"chore/CHORE-111-cleanup", "CHORE-111"},
		{"TASK-999", "TASK-999"},
		{"release/REL-222", "REL-222"},
		{"master", ""},
		{"develop", ""},
		{"feature/new-feature", ""},
	}

	for _, tt := range tests {
		t.Run(tt.branch, func(t *testing.T) {
			result := detector.detectJiraID(tt.branch)
			if result != tt.expected {
				t.Errorf("detectJiraID(%q) = %q, want %q", tt.branch, result, tt.expected)
			}
		})
	}
}

func TestJiraTaskDetectorBasicAPI(t *testing.T) {
	tests := []struct {
		name          string
		position      JiraTaskPosition
		style         JiraTaskStyle
		branch        string
		commitMessage string
		expected      string
		shouldChange  bool
	}{
		// Brackets with different positions
		{
			name:          "brackets prefix",
			position:      JiraTaskPositionPrefix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/TASK-123-new-feature",
			commitMessage: "feat(api): implement endpoint",
			expected:      "[TASK-123] feat(api): implement endpoint",
			shouldChange:  true,
		},
		{
			name:          "brackets infix",
			position:      JiraTaskPositionInfix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/TASK-123-new-feature",
			commitMessage: "feat(api): implement endpoint",
			expected:      "feat(api): [TASK-123] implement endpoint",
			shouldChange:  true,
		},
		{
			name:          "brackets suffix",
			position:      JiraTaskPositionSuffix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/TASK-123-new-feature",
			commitMessage: "feat(api): implement endpoint",
			expected:      "feat(api): implement endpoint [TASK-123]",
			shouldChange:  true,
		},
		// Parentheses with different positions
		{
			name:          "parens prefix",
			position:      JiraTaskPositionPrefix,
			style:         JiraTaskStyleParens,
			branch:        "feature/TASK-456-new-feature",
			commitMessage: "feat(api): implement endpoint",
			expected:      "(TASK-456) feat(api): implement endpoint",
			shouldChange:  true,
		},
		{
			name:          "parens infix",
			position:      JiraTaskPositionInfix,
			style:         JiraTaskStyleParens,
			branch:        "feature/TASK-456-new-feature",
			commitMessage: "feat(api): implement endpoint",
			expected:      "feat(api): (TASK-456) implement endpoint",
			shouldChange:  true,
		},
		{
			name:          "parens suffix",
			position:      JiraTaskPositionSuffix,
			style:         JiraTaskStyleParens,
			branch:        "feature/TASK-456-new-feature",
			commitMessage: "feat(api): implement endpoint",
			expected:      "feat(api): implement endpoint (TASK-456)",
			shouldChange:  true,
		},
		// No style (plain) with different positions
		{
			name:          "plain prefix",
			position:      JiraTaskPositionPrefix,
			style:         JiraTaskStylePlain,
			branch:        "feature/TASK-789-new-feature",
			commitMessage: "feat(api): implement endpoint",
			expected:      "TASK-789 feat(api): implement endpoint",
			shouldChange:  true,
		},
		{
			name:          "plain infix",
			position:      JiraTaskPositionInfix,
			style:         JiraTaskStylePlain,
			branch:        "feature/TASK-789-new-feature",
			commitMessage: "feat(api): implement endpoint",
			expected:      "feat(api): TASK-789 implement endpoint",
			shouldChange:  true,
		},
		{
			name:          "plain suffix",
			position:      JiraTaskPositionSuffix,
			style:         JiraTaskStylePlain,
			branch:        "feature/TASK-789-new-feature",
			commitMessage: "feat(api): implement endpoint",
			expected:      "feat(api): implement endpoint TASK-789",
			shouldChange:  true,
		},
		// Edge cases
		{
			name:          "no jira in branch",
			position:      JiraTaskPositionPrefix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/new-feature",
			commitMessage: "feat(api): implement endpoint",
			expected:      "feat(api): implement endpoint",
			shouldChange:  false,
		},
		{
			name:          "position none",
			position:      JiraTaskPositionNone,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/TASK-123-new-feature",
			commitMessage: "feat(api): implement endpoint",
			expected:      "feat(api): implement endpoint",
			shouldChange:  false,
		},
		{
			name:          "jira already in message",
			position:      JiraTaskPositionPrefix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/TASK-999-new-feature",
			commitMessage: "feat(api): implement TASK-999 endpoint",
			expected:      "feat(api): implement TASK-999 endpoint",
			shouldChange:  true,
		},
		{
			name:          "simple message without conventional format - brackets infix",
			position:      JiraTaskPositionInfix,
			style:         JiraTaskStyleBrackets,
			branch:        "feature/PROJ-111-new-feature",
			commitMessage: "simple commit message",
			expected:      "[PROJ-111] simple commit message",
			shouldChange:  true,
		},
		{
			name:          "multiline message",
			position:      JiraTaskPositionSuffix,
			style:         JiraTaskStyleParens,
			branch:        "bugfix/BUG-222-fix",
			commitMessage: "fix: resolve issue\n\nDetailed description",
			expected:      "fix: resolve issue (BUG-222)\n\nDetailed description",
			shouldChange:  true,
		},
		{
			name:          "plain-colon prefix",
			position:      JiraTaskPositionPrefix,
			style:         JiraTaskStylePlainColon,
			branch:        "TASK-2224_some_branch_name",
			commitMessage: "feat(api): add new endpoint",
			expected:      "TASK-2224: feat(api): add new endpoint",
			shouldChange:  true,
		},
	}

	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector := NewJIRATaskDetector(tt.position, tt.style)
			result, changed, err := detector.TransformCommitMessage(ctx, tt.branch, tt.commitMessage)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if changed != tt.shouldChange {
				t.Errorf("expected changed=%v, got %v", tt.shouldChange, changed)
			}

			if result != tt.expected {
				t.Errorf("expected message %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestJiraPositionStyleCombinations(t *testing.T) {
	branch := "feature/TEST-999-feature"
	message := "fix: resolve issue"

	tests := []struct {
		position JiraTaskPosition
		style    JiraTaskStyle
		expected string
	}{
		// All bracket combinations
		{JiraTaskPositionPrefix, JiraTaskStyleBrackets, "[TEST-999] fix: resolve issue"},
		{JiraTaskPositionInfix, JiraTaskStyleBrackets, "fix: [TEST-999] resolve issue"},
		{JiraTaskPositionSuffix, JiraTaskStyleBrackets, "fix: resolve issue [TEST-999]"},

		// All paren combinations
		{JiraTaskPositionPrefix, JiraTaskStyleParens, "(TEST-999) fix: resolve issue"},
		{JiraTaskPositionInfix, JiraTaskStyleParens, "fix: (TEST-999) resolve issue"},
		{JiraTaskPositionSuffix, JiraTaskStyleParens, "fix: resolve issue (TEST-999)"},

		// All plain combinations
		{JiraTaskPositionPrefix, JiraTaskStylePlain, "TEST-999 fix: resolve issue"},
		{JiraTaskPositionInfix, JiraTaskStylePlain, "fix: TEST-999 resolve issue"},
		{JiraTaskPositionSuffix, JiraTaskStylePlain, "fix: resolve issue TEST-999"},

		// All plain-colon  combinations
		{JiraTaskPositionPrefix, JiraTaskStylePlainColon, "TEST-999: fix: resolve issue"},
		{JiraTaskPositionInfix, JiraTaskStylePlainColon, "fix: TEST-999 resolve issue"},
		{JiraTaskPositionSuffix, JiraTaskStylePlainColon, "fix: resolve issue TEST-999"},
	}

	ctx := context.Background()
	for _, tt := range tests {
		name := string(tt.position) + "_" + string(tt.style)
		t.Run(name, func(t *testing.T) {
			detector := NewJIRATaskDetector(tt.position, tt.style)
			result, changed, err := detector.TransformCommitMessage(ctx, branch, message)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !changed {
				t.Error("expected message to be changed")
			}

			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}
