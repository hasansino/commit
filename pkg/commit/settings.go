package commit

import (
	"fmt"
	"time"
)

type Settings struct {
	Providers          []string      // AI providers to use for commit message generation
	Timeout            time.Duration // Timeout for API requests
	CustomPrompt       string        // Custom prompt template for commit messages
	First              bool          // Use the first received message and discard others
	Auto               bool          // Auto-commit with the first suggestion, no interactive mode
	DryRun             bool          // Show what would be committed without actually committing
	ExcludePatterns    []string      // File patterns to exclude from the commit
	IncludePatterns    []string      // File patterns to include in the commit
	Modules            []string      // List of modules to enable
	MultiLine          bool          // Use multi-line commit messages
	Push               bool          // Push after commit
	Tag                string        // Tag increment type: major, minor, or patch
	UseGlobalGitignore bool          // Use global gitignore from git config core.excludesFile
	MaxDiffSizeBytes   int           // Maximum diff size in bytes to consider for commit message generation
}

func (o *Settings) Validate() error {
	if o == nil {
		return fmt.Errorf("options cannot be nil")
	}
	if o.Timeout <= 0 {
		return fmt.Errorf("timeout must be greater than zero")
	}
	if o.Tag != "" && o.Tag != "major" && o.Tag != "minor" && o.Tag != "patch" {
		return fmt.Errorf("invalid tag increment type: %s (must be major, minor, or patch)", o.Tag)
	}
	return nil
}
