package ui

import (
	"context"
	"errors"
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestResolveInteractiveUIResult(t *testing.T) {
	tests := []struct {
		name         string
		model        tea.Model
		runErr       error
		wantCanceled bool
		wantErr      bool
	}{
		{
			name:         "quit without selection",
			model:        Model{},
			wantCanceled: true,
			wantErr:      true,
		},
		{
			name:         "SIGINT",
			runErr:       tea.ErrInterrupted,
			wantCanceled: true,
			wantErr:      true,
		},
		{
			name:         "external context cancellation",
			runErr:       fmt.Errorf("%w: %w", tea.ErrProgramKilled, context.Canceled),
			wantCanceled: true,
			wantErr:      true,
		},
		{
			name:    "runtime failure",
			runErr:  errors.New("terminal failure"),
			wantErr: true,
		},
		{
			name:  "completed selection",
			model: Model{done: true, finalChoice: "feat: message"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, err := resolveInteractiveUIResult(tt.model, tt.runErr)
			if (err != nil) != tt.wantErr {
				t.Fatalf("resolveInteractiveUIResult() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got := errors.Is(err, context.Canceled); got != tt.wantCanceled {
				t.Fatalf("errors.Is(error, context.Canceled) = %v, want %v", got, tt.wantCanceled)
			}
			if tt.wantErr {
				if model != nil {
					t.Fatalf("resolveInteractiveUIResult() model = %#v, want nil", model)
				}
				return
			}
			if model == nil || model.GetFinalChoice() != "feat: message" {
				t.Fatalf("resolveInteractiveUIResult() model = %#v", model)
			}
		})
	}
}
