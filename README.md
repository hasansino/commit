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

- Generates commit messages using multiple providers
- Supports multi-line commit messages
- Exclude/include specific file patterns
- Supports semantic versioning tag (major, minor, patch) incrementation
- Option to push changes after committing to relevant remote branch
- Dry-run mode
- GPG signing according to user git configuration, supporting password input
- Detects JIRA issue keys in branch name and adds them to commit message

## Demo

![Demo](./demo.gif)

## Usage

```bash
Commit helper tool

Usage:
  commit [flags]
  commit [command]

Available Commands:
  help        Help about any command
  version     Version information

Flags:
      --auto                        Auto-commit with first suggestion
      --dry-run                     Show what would be committed without committing
      --exclude strings             Exclude patterns
      --first                       Use first received message and discard others
      --include-only strings        Only include specific patterns
      --jira-transform-type string  Jira commit message transformation type: prefix|suffix|none. (default "none")
      --log-level string            Logging level (debug, info, warn, error) (default "info")
      --max-diff-size-bytes int     Maximum diff size in bytes to include in prompts (default 262144)
      --multi-line                  Use multi-line commit messages (default true)
      --prompt string               Custom prompt template
      --providers strings           Providers to use, leave empty to for all (claude|openai|gemini)
      --push                        Push after committing
      --tag string                  Create and increment semver tag part (major|minor|patch)
      --timeout duration            API timeout (default 10s)
      --use-global-gitignore        Use global gitignore (default true)

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
