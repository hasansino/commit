package ui

import (
	"context"
	"errors"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// RenderInteractiveUI runs the interactive terminal UI for commit suggestions
func RenderInteractiveUI(
	ctx context.Context,
	suggestions map[string]string,
	checkboxStates map[string]bool,
) (*Model, error) {
	program := tea.NewProgram(
		newModel(suggestions, checkboxStates),
		tea.WithContext(ctx),
		tea.WithAltScreen(), // keeps the terminal clean after exiting
	)

	runResult, err := program.Run()
	return resolveInteractiveUIResult(runResult, err)
}

func resolveInteractiveUIResult(runResult tea.Model, runErr error) (*Model, error) {
	if errors.Is(runErr, tea.ErrInterrupted) {
		return nil, fmt.Errorf("interactive ui canceled: %w", context.Canceled)
	}

	if runErr != nil {
		return nil, fmt.Errorf("failed to run interactive ui: %w", runErr)
	}

	finalState, ok := runResult.(Model)
	if !ok {
		return nil, fmt.Errorf("invalid model type returned from ui")
	}

	if !finalState.IsDone() {
		return nil, fmt.Errorf("interactive ui canceled: %w", context.Canceled)
	}

	return &finalState, nil
}
