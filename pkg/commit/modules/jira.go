package modules

import (
	"context"
	"regexp"
	"strings"
)

type JiraTransformType string

const JiraModuleName = "jira_task_detector"

const (
	JiraTransformTypeNone   JiraTransformType = "none"
	JiraTransformTypePrefix JiraTransformType = "prefix"
	JiraTransformTypeSuffix JiraTransformType = "suffix"
)

var jiraPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^([A-Z]+-\d+)`),
	regexp.MustCompile(`^feature/([A-Z]+-\d+)(?:-.*)?$`),
	regexp.MustCompile(`^bugfix/([A-Z]+-\d+)(?:-.*)?$`),
	regexp.MustCompile(`^hotfix/([A-Z]+-\d+)(?:-.*)?$`),
	regexp.MustCompile(`^chore/([A-Z]+-\d+)(?:-.*)?$`),
	regexp.MustCompile(`/([A-Z]+-\d+)(?:-|$)`),
}

type JIRATaskDetector struct {
	commitMsgTransformType JiraTransformType
}

func NewJIRATaskDetector(msgTransformType JiraTransformType) *JIRATaskDetector {
	return &JIRATaskDetector{
		commitMsgTransformType: msgTransformType,
	}
}

func (j *JIRATaskDetector) Name() string {
	return JiraModuleName
}

func (j *JIRATaskDetector) TransformPrompt(_ context.Context, prompt string) (string, bool, error) {
	return prompt, false, nil
}
func (j *JIRATaskDetector) TransformCommitMessage(_ context.Context, branch, message string) (string, bool, error) {
	if j.commitMsgTransformType == JiraTransformTypeNone {
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

func (j *JIRATaskDetector) addJiraID(commitMessage, jiraID string) string {
	if jiraID == "" {
		return commitMessage
	}
	if strings.Contains(commitMessage, "("+jiraID+")") || strings.HasPrefix(commitMessage, jiraID+": ") {
		return commitMessage
	}

	switch j.commitMsgTransformType {
	case JiraTransformTypePrefix:
		// Add as prefix: JIRA-123: message
		lines := strings.SplitN(commitMessage, "\n", 2)
		lines[0] = jiraID + ": " + lines[0]
		return strings.Join(lines, "\n")
	case JiraTransformTypeSuffix:
		// Add as suffix: message (JIRA-123)
		lines := strings.SplitN(commitMessage, "\n", 2)
		lines[0] = lines[0] + " (" + jiraID + ")"
		return strings.Join(lines, "\n")
	default:
		return commitMessage
	}
}
