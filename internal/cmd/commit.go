package cmd

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/spf13/cobra"

	"github.com/hasansino/commit/internal/cmdutil"
	"github.com/hasansino/commit/pkg/commit"
)

func newCommitCommand(f *cmdutil.Factory) *cobra.Command {
	settings := new(commit.Settings)

	cmd := &cobra.Command{
		Use:   "commit",
		Short: "Git commit automation",
		Long:  `Git commit automation`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCommitCommand(f, settings)
		},
	}

	flags := cmd.Flags()

	flags.StringSliceVar(
		&settings.Providers, "providers", []string{},
		"Providers to use, leave empty to use all available.",
	)
	flags.DurationVar(&settings.Timeout, "timeout", 10*time.Second, "API timeout")
	flags.StringVar(&settings.CustomPrompt, "prompt", "", "Custom prompt template")
	flags.BoolVar(&settings.First, "first", false, "Use first received message and discard others")
	flags.BoolVar(&settings.Auto, "auto", false, "Auto-commit with first suggestion")
	flags.BoolVar(&settings.DryRun, "dry-run", false, "Show what would be committed without committing")
	flags.StringSliceVar(&settings.ExcludePatterns, "exclude", nil, "Exclude patterns (can be used multiple times)")
	flags.StringSliceVar(&settings.IncludePatterns, "include-only", nil, "Only include specific patterns")
	flags.StringSliceVar(&settings.Modules, "modules", nil, "Modules to enable")
	flags.BoolVar(&settings.MultiLine, "multi-line", false, "Use multi-line commit messages")
	flags.BoolVar(&settings.Push, "push", false, "Push after committing")
	flags.StringVar(&settings.Tag, "tag", "", "Create and increment semver tag (major|minor|patch)")
	flags.BoolVar(&settings.UseGlobalGitignore, "use-global-gitignore", true, "Use global gitignore")

	return cmd
}

func runCommitCommand(f *cmdutil.Factory, settings *commit.Settings) error {
	service, err := commit.NewCommitService(
		settings,
		commit.WithLogger(slog.Default()),
	)
	if err != nil {
		return fmt.Errorf("failed to initialize commit service: %w", err)
	}
	return service.Execute(f.Context())
}
