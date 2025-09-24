package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lmittmann/tint"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/hasansino/commit/internal/cmdutil"
	"github.com/hasansino/commit/pkg/commit"
)

const envPrefix = "COMMIT"

const (
	exitOK    = 0
	exitError = 1
)

func NewCommitCommand(ctx context.Context, f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "commit",
		Short: "Commit helper tool",
		Long:  `Commit helper tool`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			initLogging(f.Options().LogLevel)
			return viper.BindPFlags(cmd.Flags())
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			settings := &commit.Settings{
				Providers:          viper.GetStringSlice("providers"),
				Timeout:            viper.GetDuration("timeout"),
				CustomPrompt:       viper.GetString("prompt"),
				First:              viper.GetBool("first"),
				Auto:               viper.GetBool("auto"),
				DryRun:             viper.GetBool("dry-run"),
				ExcludePatterns:    viper.GetStringSlice("exclude"),
				IncludePatterns:    viper.GetStringSlice("include-only"),
				MultiLine:          viper.GetBool("multi-line"),
				Push:               viper.GetBool("push"),
				Tag:                viper.GetString("tag"),
				UseGlobalGitignore: viper.GetBool("use-global-gitignore"),
				MaxDiffSizeBytes:   viper.GetInt("max-diff-size-bytes"),
				JiraTaskPosition:   viper.GetString("jira-task-position"),
				JiraTaskStyle:      viper.GetString("jira-task-style"),
			}
			return runCommitCommand(f, settings)
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

	viper.SetEnvPrefix(envPrefix)
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()

	f.BindFlags(cmd.PersistentFlags())

	flags := cmd.Flags()

	flags.StringSlice("providers", []string{},
		"Providers to use, leave empty for all (claude|openai|gemini).")
	flags.Duration("timeout", 10*time.Second,
		"API timeout.")
	flags.String("prompt", "",
		"Custom prompt template.")
	flags.Bool("first", false,
		"Use first received message and discard others.")
	flags.Bool("auto", false,
		"Auto-commit with first suggestion.")
	flags.Bool("dry-run", false,
		"Show what would be committed without committing.")
	flags.StringSlice("exclude", nil,
		"Exclude patterns.")
	flags.StringSlice("include-only", nil,
		"Only include specific patterns.")
	flags.Bool("multi-line", false,
		"Use multi-line commit messages.")
	flags.Bool("push", false,
		"Push after committing.")
	flags.String("tag", "",
		"Create and increment semver tag part (major|minor|patch).")
	flags.Bool("use-global-gitignore", true,
		"Use global gitignore.")
	flags.Int("max-diff-size-bytes", 64*1024, // 64KB
		"Maximum diff size in bytes to include in prompts.")
	flags.String("jira-task-position", "none",
		"Jira task position in commit message: prefix, infix, suffix, or none.")
	flags.String("jira-task-style", "none",
		"Jira task style: brackets (e.g., [TASK-123]), parens (e.g., (TASK-123)), or none (e.g., TASK-123).")

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
