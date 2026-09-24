package local

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const maxCommandErrorLength = 2 * 1024

func generateWithLlamaCLI(
	ctx context.Context,
	executable string,
	modelPath string,
	prompt string,
	contextSize uint32,
	maxTokens int,
	timeout time.Duration,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	promptPath, removePrompt, err := writeTemporaryFile("commit-llama-prompt-*", prompt)
	if err != nil {
		return "", fmt.Errorf("create llama-cli prompt file: %w", err)
	}
	defer removePrompt()

	outputFile, err := os.CreateTemp("", "commit-llama-output-*")
	if err != nil {
		return "", fmt.Errorf("create llama-cli output file: %w", err)
	}
	outputPath := outputFile.Name()
	defer func() { _ = os.Remove(outputPath) }()
	if err := outputFile.Close(); err != nil {
		return "", fmt.Errorf("close llama-cli output file: %w", err)
	}

	inferenceCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{
		"--model", modelPath,
		"--file", promptPath,
		"--output-file", outputPath,
		"--ctx-size", strconv.FormatUint(uint64(contextSize), 10),
		"--n-predict", strconv.Itoa(maxTokens),
		"--temperature", "0.2",
		"--top-p", "0.9",
		"--min-p", "0",
		"--repeat-penalty", "1.05",
		"--repeat-last-n", "64",
		"--presence-penalty", "0.1",
		"--frequency-penalty", "0.1",
		"--reasoning", "off",
		"--single-turn",
		"--simple-io",
		"--no-escape",
		"--no-display-prompt",
		"--no-show-timings",
		"--no-warmup",
		"--color", "off",
		"--log-disable",
		"--offline",
	}

	// #nosec G204 -- llama-cli is resolved from the user's PATH; arguments never pass through a shell.
	command := exec.CommandContext(inferenceCtx, executable, args...)
	var stderr bytes.Buffer
	command.Stdout = io.Discard
	command.Stderr = &stderr

	if err := command.Run(); err != nil {
		if ctxErr := inferenceCtx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", formatCommandError(err, stderr.String())
	}

	// #nosec G304 -- outputPath is the private file created above with os.CreateTemp.
	output, err := os.ReadFile(outputPath)
	if err != nil {
		return "", fmt.Errorf("read llama-cli output: %w", err)
	}

	return parseLlamaCLIOutput(string(output), prompt)
}

func writeTemporaryFile(pattern, content string) (string, func(), error) {
	file, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", func() {}, err
	}
	path := file.Name()
	remove := func() { _ = os.Remove(path) }

	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		remove()
		return "", func() {}, err
	}
	if err := file.Close(); err != nil {
		remove()
		return "", func() {}, err
	}

	return path, remove, nil
}

func parseLlamaCLIOutput(output, prompt string) (string, error) {
	output = strings.ReplaceAll(output, "\r\n", "\n")
	prompt = strings.ReplaceAll(prompt, "\r\n", "\n")

	const userPrefix = "User:\n"
	assistantMarkers := [...]string{"\n\nAssistant:\n", "\nAssistant:\n", "Assistant:\n"}

	if strings.HasPrefix(output, userPrefix) {
		promptEnd := len(userPrefix) + len(prompt)
		if !strings.HasPrefix(output, userPrefix+prompt) {
			return "", fmt.Errorf("llama-cli returned an unexpected conversation transcript")
		}
		transcript := output[promptEnd:]
		assistantMarker := ""
		for _, marker := range assistantMarkers {
			if strings.HasPrefix(transcript, marker) {
				assistantMarker = marker
				break
			}
		}
		if assistantMarker == "" {
			return "", fmt.Errorf("llama-cli transcript is missing the assistant response")
		}
		output = transcript[len(assistantMarker):]
	}

	result := strings.TrimSpace(output)
	if result == "" {
		return "", fmt.Errorf("llama-cli returned no content")
	}
	return result, nil
}

func formatCommandError(runErr error, stderr string) error {
	details := strings.TrimSpace(stderr)
	if len(details) > maxCommandErrorLength {
		details = details[:maxCommandErrorLength] + "..."
	}
	if details == "" {
		return fmt.Errorf("llama-cli failed: %w", runErr)
	}
	return fmt.Errorf("llama-cli failed: %w: %s", runErr, details)
}
