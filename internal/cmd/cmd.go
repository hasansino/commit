package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lmittmann/tint"
	"github.com/spf13/cobra"

	"github.com/hasansino/commit/internal/cmdutil"
	"github.com/hasansino/commit/pkg/commit"
)

const (
	exitOK    = 0
	exitError = 1
)

func NewCommitCommand(ctx context.Context, f *cmdutil.Factory) *cobra.Command {
	settings := new(commit.Settings)
	cmd := &cobra.Command{
		Use:   "commit",
		Short: "Commit helper tool",
		Long:  `Commit helper tool`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCommitCommand(f, settings)
		},
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			initLogging(f.Options().LogLevel)
		},
		SilenceUsage:  true,
		SilenceErrors: false,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
			HiddenDefaultCmd:  true,
		},
	}

	cmd.SetContext(ctx)
	cmd.SetIn(os.Stdin)
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)

	f.BindFlags(cmd.PersistentFlags())

	flags := cmd.Flags()

	flags.StringSliceVar(
		&settings.Providers, "providers", []string{},
		"Providers to use, leave empty to for all (claude|openai|gemini)")
	flags.DurationVar(
		&settings.Timeout, "timeout", 10*time.Second, "API timeout")
	flags.StringVar(
		&settings.CustomPrompt, "prompt", "", "Custom prompt template")
	flags.BoolVar(
		&settings.First, "first", false, "Use first received message and discard others")
	flags.BoolVar(
		&settings.Auto, "auto", false, "Auto-commit with first suggestion")
	flags.BoolVar(
		&settings.DryRun, "dry-run", false, "Show what would be committed without committing")
	flags.StringSliceVar(
		&settings.ExcludePatterns, "exclude", nil, "Exclude patterns")
	flags.StringSliceVar(
		&settings.IncludePatterns, "include-only", nil, "Only include specific patterns")
	flags.StringSliceVar(
		&settings.Modules, "modules", []string{"jira"}, "Modules to enable")
	flags.BoolVar(
		&settings.MultiLine, "multi-line", true, "Use multi-line commit messages")
	flags.BoolVar(
		&settings.Push, "push", false, "Push after committing")
	flags.StringVar(
		&settings.Tag, "tag", "", "Create and increment semver tag part (major|minor|patch)")
	flags.BoolVar(
		&settings.UseGlobalGitignore, "use-global-gitignore", true, "Use global gitignore")
	flags.IntVar(
		&settings.MaxDiffSizeBytes, "max-diff-size-bytes", 256*1024, //256KB
		"Maximum diff size in bytes to include in prompts")

	cmd.AddCommand(newVersionCommand())

	return cmd
}

func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	factory := cmdutil.NewFactory(ctx)
	cmd := NewCommitCommand(ctx, factory)

	var execErr error
	cmd, execErr = cmd.ExecuteContextC(ctx)

	if execErr != nil {
		if cmd != nil && cmd.SilenceErrors {
			return exitOK
		}
		return exitError
	}

	return exitOK
}

func initLogging(level string) {
	var slogLevel slog.Level
	switch level {
	case "debug":
		slogLevel = slog.LevelDebug
	case "info":
		slogLevel = slog.LevelInfo
	case "warn":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}

	loggerOpts := &tint.Options{
		AddSource:  false,
		Level:      slogLevel,
		TimeFormat: time.TimeOnly,
	}

	logger := slog.New(tint.NewHandler(os.Stdout, loggerOpts))

	// Any call to log.* will be redirected to slog.Error.
	// Because of that, we need to agree to use `log` package only for errors.
	slog.SetLogLoggerLevel(slog.LevelError)

	// for both 'log' and 'slog'
	slog.SetDefault(logger)
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
