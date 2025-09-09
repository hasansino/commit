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
func (j *JIRAPrefixDetector) TransformCommitMessage(_ context.Context, message string) (string, bool, error) {
	jiraPrefix := j.detectJIRAPrefix(message)
	if jiraPrefix == "" {
		return message, false, nil
	}
	commitMessage := j.applyJIRAPrefix(message, jiraPrefix)
	return commitMessage, true, nil
}

func (j *JIRAPrefixDetector) detectJIRAPrefix(branchName string) string {
	for _, pattern := range jiraPatterns {
		matches := pattern.FindStringSubmatch(branchName)
		if len(matches) > 1 && matches[1] != "" {
			return matches[1] + ": "
		}
	}
	return ""
}

func (j *JIRAPrefixDetector) applyJIRAPrefix(commitMessage, jiraPrefix string) string {
	if jiraPrefix == "" {
		return commitMessage
	}
	if strings.HasPrefix(commitMessage, jiraPrefix) {
		return commitMessage
	}
	return jiraPrefix + commitMessage
}
