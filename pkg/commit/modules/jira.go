package modules

import (
	"context"
	"regexp"
	"strings"
)

var jiraPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^([A-Z]+-\d+)`),
	regexp.MustCompile(`^feature/([A-Z]+-\d+)(?:-.*)?$`),
	regexp.MustCompile(`^bugfix/([A-Z]+-\d+)(?:-.*)?$`),
	regexp.MustCompile(`^hotfix/([A-Z]+-\d+)(?:-.*)?$`),
	regexp.MustCompile(`^chore/([A-Z]+-\d+)(?:-.*)?$`),
	regexp.MustCompile(`/([A-Z]+-\d+)(?:-|$)`),
}

type JIRAPrefixDetector struct{}

func NewJIRAPrefixDetector() *JIRAPrefixDetector {
	return &JIRAPrefixDetector{}
}

func (j *JIRAPrefixDetector) Name() string {
	return "jira"
}

func (j *JIRAPrefixDetector) TransformPrompt(_ context.Context, prompt string) (string, bool, error) {
	return prompt, false, nil
}
func (j *JIRAPrefixDetector) TransformCommitMessage(_ context.Context, branch, message string) (string, bool, error) {
	jiraPrefix := j.detectJiraID(branch)
	if jiraPrefix == "" {
		return message, false, nil
	}
	commitMessage := j.addJiraID(message, jiraPrefix)
	return commitMessage, true, nil
}

func (j *JIRAPrefixDetector) detectJiraID(branchName string) string {
	for _, pattern := range jiraPatterns {
		matches := pattern.FindStringSubmatch(branchName)
		if len(matches) > 1 && matches[1] != "" {
			return matches[1]
		}
	}
	return ""
}

func (j *JIRAPrefixDetector) addJiraID(commitMessage, jiraID string) string {
	if jiraID == "" {
		return commitMessage
	}

	if strings.Contains(commitMessage, "("+jiraID+")") {
		return commitMessage
	}

	lines := strings.SplitN(commitMessage, "\n", 2)
	if len(lines) == 1 {
		return commitMessage + " (" + jiraID + ")"
	}

	lines[0] = lines[0] + " (" + jiraID + ")"
	return strings.Join(lines, "\n")
}
