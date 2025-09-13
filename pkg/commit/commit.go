package commit

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/hasansino/commit/pkg/commit/modules"
	"github.com/hasansino/commit/pkg/commit/ui"
)

const defaultRepoPath = "."

type Service struct {
	logger    *slog.Logger
	settings  *Settings
	gitOps    *gitOperations
	aiService *aiService
	modules   []moduleAccessor
}

func NewCommitService(settings *Settings, opts ...Option) (*Service, error) {
	if err := settings.Validate(); err != nil {
		return nil, fmt.Errorf("invalid options: %w", err)
	}

	svc := &Service{
		settings: settings,
		modules:  make([]moduleAccessor, 0),
	}

	for _, opt := range opts {
		opt(svc)
	}

	if svc.logger == nil {
		svc.logger = slog.New(slog.DiscardHandler)
	}

	git, err := newGitOperations(defaultRepoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize git operations: %w", err)
	}

	svc.gitOps = git
	svc.aiService = newAIService(svc.logger, settings.Timeout)

	for _, name := range settings.Modules {
		switch name {
		case "jira":
			svc.modules = append(svc.modules, modules.NewJIRAPrefixDetector())
		}
	}

	return svc, nil
}

func (s *Service) Execute(ctx context.Context) error {
	if len(s.aiService.GetProviders()) == 0 {
		s.logger.WarnContext(ctx, "No providers configured")
		return fmt.Errorf("no api keys found in environment")
	}

	if !s.gitOps.IsGitRepository() {
		return fmt.Errorf("not a git repository")
	}

	s.logger.DebugContext(ctx, "Unstaging all files...")

	if err := s.gitOps.UnstageAll(); err != nil {
		s.logger.ErrorContext(ctx, "Failed to unstage files", "error", err)
		return fmt.Errorf("failed to unstage files: %w", err)
	}

	s.logger.DebugContext(ctx, "Staging files...")

	stagedFiles, err := s.gitOps.StageFiles(
		s.settings.ExcludePatterns,
		s.settings.IncludePatterns,
		s.settings.UseGlobalGitignore,
	)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to stage files", "error", err)
		return fmt.Errorf("failed to stage files: %w", err)
	}

	if len(stagedFiles) == 0 {
		s.logger.WarnContext(ctx, "No files to commit")
		return nil
	}

	s.logger.DebugContext(ctx, "Getting staged diff...")

	diff, err := s.gitOps.GetStagedDiff()
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to get staged diff", "error", err)
		return fmt.Errorf("failed to get diff: %w", err)
	}

	if strings.TrimSpace(diff) == "" {
		s.logger.WarnContext(ctx, "No changes staged for commit")
		return nil
	}

	branch, err := s.gitOps.GetCurrentBranch()
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to get current branch", "error", err)
		return fmt.Errorf("failed to get current branch: %w", err)
	}

	s.logger.DebugContext(ctx, "Requesting commit messages...")

	messages, err := s.aiService.GenerateCommitMessages(
		ctx,
		diff, branch, stagedFiles,
		s.settings.Providers, s.settings.CustomPrompt,
		s.settings.First, s.settings.MultiLine,
	)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to generate commit messages", "error", err)
		return fmt.Errorf("failed to generate suggestions: %w", err)
	}

	var commitMessage string

	if s.settings.Auto {
		commitMessage = s.getRandomMessage(messages)
		if commitMessage == "" {
			s.logger.WarnContext(ctx, "No valid suggestions available for auto-commit")
			return fmt.Errorf("no valid suggestions available for auto-commit")
		}
		s.logger.DebugContext(ctx, "Auto-selected commit message", "message", commitMessage)
	} else {
		s.logger.DebugContext(ctx, "Using interactive mode...")

		uiModel, err := ui.RenderInteractiveUI(
			ctx,
			messages,
			map[string]bool{
				ui.CheckboxIDDryRun:         s.settings.DryRun,
				ui.CheckboxIDPush:           !s.settings.DryRun && s.settings.Push,
				ui.CheckboxIDCreateTagMajor: !s.settings.DryRun && s.settings.Tag == "major",
				ui.CheckboxIDCreateTagMinor: !s.settings.DryRun && s.settings.Tag == "minor",
				ui.CheckboxIDCreateTagPatch: !s.settings.DryRun && s.settings.Tag == "patch",
			},
		)
		if err != nil {
			s.logger.ErrorContext(ctx, "Failed to enter interactive mode", "error", err)
			return fmt.Errorf("failed to run interactive ui: %w", err)
		}

		commitMessage = uiModel.GetFinalChoice()

		// override flags if user interacted with checkboxes
		s.settings.DryRun = uiModel.GetCheckboxValue(ui.CheckboxIDDryRun)
		s.settings.Push = uiModel.GetCheckboxValue(ui.CheckboxIDPush)

		s.settings.Tag = ""
		if uiModel.GetCheckboxValue(ui.CheckboxIDCreateTagMajor) {
			s.settings.Tag = "major"
		}
		if uiModel.GetCheckboxValue(ui.CheckboxIDCreateTagMinor) {
			s.settings.Tag = "minor"
		}
		if uiModel.GetCheckboxValue(ui.CheckboxIDCreateTagPatch) {
			s.settings.Tag = "patch"
		}
	}

	if len(commitMessage) == 0 {
		s.logger.WarnContext(ctx, "No commit message provided")
		return fmt.Errorf("no commit message provided")
	}

	branch, err = s.gitOps.GetCurrentBranch()
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to get current branch", "error", err)
		return fmt.Errorf("failed to get current branch: %w", err)
	}

	for _, module := range s.modules {
		s.logger.DebugContext(ctx, "Running module", "name", module.Name())
		commitMessage, workDone, err := module.TransformCommitMessage(ctx, branch, commitMessage)
		if !workDone {
			s.logger.DebugContext(
				ctx, "Module did not transform commit message",
				"module", module.Name(),
			)
			continue
		}
		if err != nil {
			s.logger.ErrorContext(
				ctx, "Failed to transform commit message",
				"module", module.Name(),
				"error", err,
			)
			continue
		}
		s.logger.DebugContext(
			ctx, "Transformed commit message",
			"module", module.Name(),
			"message", commitMessage,
		)
	}

	commitMessage = strings.Trim(commitMessage, "\n")
	commitMessage = strings.TrimSpace(commitMessage)

	if !s.settings.DryRun {
		if err := s.gitOps.CreateCommit(commitMessage); err != nil {
			s.logger.ErrorContext(ctx, "Failed to create commit", "error", err)
			return fmt.Errorf("failed to create commit: %w", err)
		}
		s.logger.InfoContext(
			ctx, "Commit created",
			"commit_message", commitMessage,
		)

		if s.settings.Push {
			mrURL, err := s.gitOps.Push()
			if err != nil {
				s.logger.ErrorContext(ctx, "Failed to push to remote", "error", err)
				return fmt.Errorf("failed to push: %w", err)
			}
			s.logger.InfoContext(ctx, "Successfully pushed to remote")

			if mrURL != "" {
				s.logger.InfoContext(ctx, "Create merge/pull request", "url", mrURL)
			}
		}

		if s.settings.Tag != "" {
			latestTag, err := s.gitOps.GetLatestTag()
			if err != nil {
				s.logger.ErrorContext(ctx, "Failed to get latest tag", "error", err)
				return fmt.Errorf("failed to get latest tag: %w", err)
			}

			if latestTag == "" {
				s.logger.WarnContext(ctx, "No existing tags found, will create first tag")
			} else {
				s.logger.InfoContext(ctx, "Latest tag found", "tag", latestTag)
			}

			newTag, err := s.gitOps.IncrementVersion(latestTag, s.settings.Tag)
			if err != nil {
				s.logger.ErrorContext(ctx, "Failed to increment version", "error", err)
				return fmt.Errorf("failed to increment version: %w", err)
			}

			if err := s.gitOps.CreateTag(newTag, commitMessage); err != nil {
				s.logger.ErrorContext(ctx, "Failed to create tag", "tag", newTag, "error", err)
				return fmt.Errorf("failed to create tag %s: %w", newTag, err)
			}

			s.logger.InfoContext(ctx, "Tag created", "tag", newTag)

			if s.settings.Push {
				if err := s.gitOps.PushTag(newTag); err != nil {
					s.logger.ErrorContext(ctx, "Failed to push tag", "tag", newTag, "error", err)
					return fmt.Errorf("failed to push tag %s: %w", newTag, err)
				}
				s.logger.InfoContext(ctx, "Tag pushed to remote", "tag", newTag)
			}
		}
	} else {
		s.logger.WarnContext(ctx, "Dry run enabled, no side effects created")
	}

	return nil
}

func (s *Service) getRandomMessage(messages map[string]string) string {
	// map provides random access, so we can just return the first message
	for _, msg := range messages {
		return msg
	}
	return ""
}
