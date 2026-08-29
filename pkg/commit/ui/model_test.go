package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestModelSelectionModeCancel(t *testing.T) {
	tests := []struct {
		name string
		key  tea.KeyMsg
	}{
		{
			name: "quit key",
			key: tea.KeyMsg{
				Type:  tea.KeyRunes,
				Runes: []rune(KeyQuit),
			},
		},
		{
			name: "interrupt key",
			key:  tea.KeyMsg{Type: tea.KeyCtrlC},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := newModel(map[string]string{"provider": "feat: message"}, nil)
			updated, cmd := updateModelWithKey(t, model, tt.key)

			if updated.IsDone() {
				t.Fatal("canceling the selection marked the model done")
			}
			if updated.GetFinalChoice() != "" {
				t.Fatalf("canceling the selection chose %q", updated.GetFinalChoice())
			}
			assertQuitCommand(t, cmd)
		})
	}
}

func TestModelManualModeCancel(t *testing.T) {
	model := newModel(nil, nil)
	model, cmd := updateModelWithKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("entering manual mode unexpectedly returned a command")
	}
	if !model.manualMode {
		t.Fatal("manual option did not enter manual mode")
	}

	model, _ = updateModelWithKey(t, model, tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune("draft"),
	})
	model, cmd = updateModelWithKey(t, model, tea.KeyMsg{Type: tea.KeyCtrlC})

	if model.IsDone() {
		t.Fatal("canceling manual mode marked the model done")
	}
	if model.GetFinalChoice() != "" {
		t.Fatalf("canceling manual mode chose %q", model.GetFinalChoice())
	}
	assertQuitCommand(t, cmd)
}

func TestModelSuccessfulSelectionIsDone(t *testing.T) {
	model := newModel(map[string]string{"provider": "feat: message"}, nil)
	model, cmd := updateModelWithKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	if !model.IsDone() {
		t.Fatal("selecting a suggestion did not mark the model done")
	}
	if got := model.GetFinalChoice(); got != "feat: message" {
		t.Fatalf("GetFinalChoice() = %q, want %q", got, "feat: message")
	}
	assertQuitCommand(t, cmd)
}

func TestModelSuccessfulManualInputIsDone(t *testing.T) {
	model := newModel(nil, nil)
	model, _ = updateModelWithKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model, _ = updateModelWithKey(t, model, tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune("feat: manual"),
	})
	model, cmd := updateModelWithKey(t, model, tea.KeyMsg{Type: tea.KeyCtrlD})

	if !model.IsDone() {
		t.Fatal("finishing manual input did not mark the model done")
	}
	if got := model.GetFinalChoice(); got != "feat: manual" {
		t.Fatalf("GetFinalChoice() = %q, want %q", got, "feat: manual")
	}
	assertQuitCommand(t, cmd)
}

func updateModelWithKey(t *testing.T, model Model, key tea.KeyMsg) (Model, tea.Cmd) {
	t.Helper()

	updated, cmd := model.Update(key)
	updatedModel, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update() returned %T, want ui.Model", updated)
	}
	return updatedModel, cmd
}

func assertQuitCommand(t *testing.T, cmd tea.Cmd) {
	t.Helper()

	if cmd == nil {
		t.Fatal("Update() returned no quit command")
	}
	if message := cmd(); message != tea.Quit() {
		t.Fatalf("command returned %T, want tea.QuitMsg", message)
	}
}
