<p align="center">
<a href="https://opensource.org/licenses/MIT"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="licence"></a>
<a href="https://golang.org/"><img src="https://img.shields.io/badge/Go-1.25.1-00ADD8?style=flat&logo=go" alt="goversion"></a>
<a href="https://goreportcard.com/report/github.com/hasansino/commit"><img src="https://goreportcard.com/badge/github.com/hasansino/commit" alt="goreport"></a>
<a href="https://github.com/hasansino/commit/releases"><img src="https://img.shields.io/github/v/release/hasansino/commit" alt="release"></a>
</p>

# commit

Commit helper tool.

## Installation

### Homebrew

```bash
brew tap hasansino/commit
brew install commit
```

### Go

```bash
go install github.com/hasansino/commit@latest
```

### Download Binary

Download the latest binary from the [releases page](https://github.com/hasansino/commit/releases).

## Features

- Dry-run mode
- Generates messages according to conventional commits specification
- Generates commit messages using multiple providers (claude, openai, gemini)
- Supports multi-line commit messages
- Exclude/include specific file patterns and use global gitignore
- Customizable commit message prompt templates
- Option to use first or fastest response from providers
- Configurable maximum diff size to include in prompts
- Supports semantic versioning tag (major, minor, patch) incrementation and push
- Option to push changes after committing to relevant remote branch
- GPG signing according to user git configuration, supporting password input
- Detects JIRA issue keys in branch name and adds them to commit message

## Demo

![Demo](./demo.gif)

## Usage

```terminaloutput
Commit helper tool

Usage:
  commit [flags]
  commit [command]

Available Commands:
  help        Help about any command
  version     Version information

Flags:
      --auto                        Auto-commit with first and fastest response from provider.
      --dry-run                     Show what would be committed without committing.
      --exclude strings             Exclude patterns, when staging changes.
      --first                       Use first received message and discard others.
  -h, --help                        help for commit
      --include-only strings        Only include specific patterns, when staging changes.
      --jira-task-position string   Jira task position in commit message: prefix, infix, suffix, or none. (default "none")
      --jira-task-style string      Jira task style: brackets, parens , plain-colon, or plain. (default "plain")
      --log-level string            Logging level (debug, info, warn, error) (default "info")
      --max-diff-size-bytes int     Maximum diff size in bytes to include in prompts. (default 65536)
      --multi-line                  Use multi-line commit messages.
      --prompt string               Custom prompt template.
      --providers strings           Providers to use, leave empty for all (claude|openai|gemini).
      --push                        Push after committing.
      --tag string                  Create and increment semver tag part (major|minor|patch).
      --timeout duration            API timeout. (default 10s)
      --use-global-gitignore        Use global gitignore. (default true)

Use "commit [command] --help" for more information about a command.
```

All flags can also be set via environment variables, e.g. `COMMIT_AUTO=true`.

## Configuration

At least one *_API_KEY variable is required to use this tool.

- ANTHROPIC_API_KEY
- ANTHROPIC_MODEL (optional, defaults to "claude-3-5-haiku-latest")
- OPENAI_API_KEY
- OPENAI_MODEL (optional, defaults to "gpt-4-turbo")
- GEMINI_API_KEY
- GEMINI_MODEL (optional, defaults to "gemini-1.5-flash")

## Custom Prompt Variables

- {diff}: git diff of the changes to be committed
- {files}: list of changed files
- {branch}: current git branch name
