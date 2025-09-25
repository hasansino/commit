package modules

import (
	"context"
	"regexp"
	"strings"
)

type JiraTaskPosition string
type JiraTaskStyle string

const JiraModuleName = "jira_task_detector"

const (
	JiraTaskPositionNone   JiraTaskPosition = "none"
	JiraTaskPositionPrefix JiraTaskPosition = "prefix"
	JiraTaskPositionInfix  JiraTaskPosition = "infix"
	JiraTaskPositionSuffix JiraTaskPosition = "suffix"
)

const (
	JiraTaskStylePlain      JiraTaskStyle = "plain"       // TASK-000
	JiraTaskStylePlainColon JiraTaskStyle = "plain_colon" // TASK-000:
	JiraTaskStyleBrackets   JiraTaskStyle = "brackets"    // [TASK-000]
	JiraTaskStyleParens     JiraTaskStyle = "parens"      // (TASK-000)
)

var jiraPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^([A-Z]+-\d+)`),
	regexp.MustCompile(`^feature/([A-Z]+-\d+)(?:-.*)?$`),
	regexp.MustCompile(`^bugfix/([A-Z]+-\d+)(?:-.*)?$`),
	regexp.MustCompile(`^hotfix/([A-Z]+-\d+)(?:-.*)?$`),
	regexp.MustCompile(`^chore/([A-Z]+-\d+)(?:-.*)?$`),
	regexp.MustCompile(`/([A-Z]+-\d+)(?:-|$)`),
}

// conventionalCommitPattern matches valid conventional commit prefixes
// Format: type[(scope)][!]
var conventionalCommitPattern = regexp.MustCompile(`^[a-z]+(\([a-zA-Z0-9\-_]+\))?!?$`)

// Common conventional commit types
var conventionalCommitTypes = map[string]bool{
	"feat":     true,
	"fix":      true,
	"docs":     true,
	"style":    true,
	"refactor": true,
	"perf":     true,
	"test":     true,
	"build":    true,
	"ci":       true,
	"chore":    true,
	"revert":   true,
}

type JIRATaskDetector struct {
	position JiraTaskPosition
	style    JiraTaskStyle
}

func NewJIRATaskDetector(position JiraTaskPosition, style JiraTaskStyle) *JIRATaskDetector {
	return &JIRATaskDetector{
		position: position,
		style:    style,
	}
}

func (j *JIRATaskDetector) Name() string {
	return JiraModuleName
}

func (j *JIRATaskDetector) TransformPrompt(_ context.Context, prompt string) (string, bool, error) {
	return prompt, false, nil
}
func (j *JIRATaskDetector) TransformCommitMessage(_ context.Context, branch, message string) (string, bool, error) {
	if j.position == JiraTaskPositionNone {
		return message, false, nil
	}

	jiraID := j.detectJiraID(branch)

	if jiraID == "" {
		return message, false, nil
	}

	return j.addJiraID(message, jiraID), true, nil
}

func (j *JIRATaskDetector) detectJiraID(branchName string) string {
	for _, pattern := range jiraPatterns {
		matches := pattern.FindStringSubmatch(branchName)
		if len(matches) > 1 && matches[1] != "" {
			return matches[1]
		}
	}
	return ""
}

// isConventionalCommitPrefix checks if a string is a valid conventional commit prefix
func isConventionalCommitPrefix(prefix string) bool {
	// Check format
	if !conventionalCommitPattern.MatchString(prefix) {
		return false
	}

	// Extract the type (part before optional scope)
	typeEnd := strings.IndexByte(prefix, '(')
	if typeEnd == -1 {
		// No scope, check if type ends with !
		if strings.HasSuffix(prefix, "!") {
			typeEnd = len(prefix) - 1
		} else {
			typeEnd = len(prefix)
		}
	}

	commitType := prefix[:typeEnd]

	// Check if it's a known conventional commit type
	return conventionalCommitTypes[commitType]
}

func (j *JIRATaskDetector) addJiraID(commitMessage, jiraID string) string {
	if jiraID == "" {
		return commitMessage
	}
	if strings.Contains(commitMessage, jiraID) {
		return commitMessage
	}

	lines := strings.SplitN(commitMessage, "\n", 2)
	firstLine := lines[0]

	// Format the JIRA ID based on style
	var formattedID string
	switch j.style {
	case JiraTaskStyleBrackets:
		formattedID = "[" + jiraID + "]"
	case JiraTaskStyleParens:
		formattedID = "(" + jiraID + ")"
	case JiraTaskStylePlainColon:
		if j.position == JiraTaskPositionPrefix {
			formattedID = jiraID + ":"
		} else {
			formattedID = jiraID
		}
	default:
		formattedID = jiraID
	}

	// Extract conventional commit type and scope if present
	var prefix, mainMessage string
	if idx := strings.Index(firstLine, ": "); idx > 0 && idx < 50 { // reasonable length for a prefix
		potentialPrefix := firstLine[:idx]
		// Check if this looks like a conventional commit
		// Valid format: type or type(scope) or type(scope)!
		if isConventionalCommitPrefix(potentialPrefix) {
			prefix = potentialPrefix
			mainMessage = firstLine[idx+2:]
		} else {
			mainMessage = firstLine
		}
	} else {
		mainMessage = firstLine
	}

	// Apply position
	switch j.position {
	case JiraTaskPositionPrefix:
		// [TASK-000] feat(api): sometext or TASK-000 feat(api): sometext
		lines[0] = formattedID + " " + firstLine
	case JiraTaskPositionInfix:
		// feat(api): [TASK-000] sometext or feat(api): TASK-000 sometext
		if prefix != "" {
			lines[0] = prefix + ": " + formattedID + " " + mainMessage
		} else {
			lines[0] = formattedID + " " + firstLine
		}
	case JiraTaskPositionSuffix:
		// feat(api): sometext [TASK-000] or feat(api): sometext TASK-000
		lines[0] = firstLine + " " + formattedID
	default:
		return commitMessage
	}

	return strings.Join(lines, "\n")
}
