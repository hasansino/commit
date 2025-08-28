package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Model represents the state of the terminal UI
type Model struct {
	list        list.Model
	delegate    *commitDelegate
	suggestions map[string]string
	choices     []list.Item
	manualMode  bool
	manualInput string
	finalChoice string
	done        bool
	width       int
	height      int
	checkboxes  map[string]bool
}

// newModel creates a new UI model with fancy list
func newModel(suggestions map[string]string, checkboxStates map[string]bool) Model {
	items := buildListItems(suggestions)

	// Create custom delegate for multi-line support
	delegateValue := newCommitDelegate()
	delegate := &delegateValue

	// Calculate appropriate height based on number of items
	listHeight := DefaultListHeight
	if len(items) > 0 {
		// Adjust height based on content
		totalHeight := len(items) * (delegate.Height())
		if totalHeight < MaxListHeight {
			listHeight = totalHeight + MinListHeight
		}
	}

	// Create the list with custom delegate
	l := list.New(items, delegate, 0, listHeight)
	l.Title = ListTitle
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.SetShowHelp(true)
	l.DisableQuitKeybindings() // We'll handle quit ourselves

	// Customize list styles
	l.Styles.Title = lipgloss.NewStyle().
		Background(lipgloss.Color(ColorAccent)).
		Foreground(lipgloss.Color(ColorBright)).
		Bold(true).
		Italic(true).
		Padding(0, 2).
		MarginBottom(1).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ColorAccent)).
		BorderBottom(true)

	l.Styles.PaginationStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorDimmed))

	l.Styles.HelpStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorMuted))

	// Custom keybindings help
	l.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{
			key.NewBinding(key.WithKeys(KeySelect), key.WithHelp(KeySelect, "select")),
			key.NewBinding(key.WithKeys(KeyQuit), key.WithHelp(KeyQuit, "quit")),
		}
	}

	// Initialize checkboxes with default values
	checkboxes := checkboxDefaults
	for k, v := range checkboxStates {
		if _, exists := checkboxes[k]; !exists {
			continue // Ignore unknown keys
		}
		checkboxes[k] = v
	}

	return Model{
		list:        l,
		delegate:    delegate,
		suggestions: suggestions,
		choices:     items,
		manualMode:  false,
		manualInput: "",
		done:        false,
		checkboxes:  checkboxes,
	}
}

// buildListItems converts suggestions to list items
func buildListItems(suggestions map[string]string) []list.Item {
	var items []list.Item

	// Add AI suggestions
	for provider, message := range suggestions {
		lines := strings.Split(strings.TrimSpace(message), "\n")
		// Clean up empty lines at the end
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		items = append(items, CommitItem{
			provider: provider,
			message:  message,
			lines:    lines,
		})
	}

	// Add manual entry option at the end
	items = append(items, CommitItem{
		provider: ProviderManual,
		message:  "",
		lines:    []string{},
	})

	return items
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
	return nil
}

// Update handles input events
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		// Update delegate width for dynamic description length
		if m.delegate != nil {
			m.delegate.SetWidth(msg.Width)
		}

		// Calculate available width accounting for padding
		availableWidth := msg.Width - (PaddingHorizontal * 2)
		if availableWidth > 0 {
			m.list.SetWidth(availableWidth)
		}

		// Calculate available height accounting for:
		// - Top padding
		// - Footer (border + checkboxes + help text)
		// - Bottom margin
		availableHeight := msg.Height - PaddingTop - FooterHeightApprox - 1
		if availableHeight > MinListHeight {
			m.list.SetHeight(availableHeight)
		} else {
			m.list.SetHeight(MinListHeight)
		}
		return m, nil
	case tea.KeyMsg:
		if m.manualMode {
			return m.updateManualMode(msg)
		}

		// Handle selection mode
		switch msg.String() {
		case KeyInterrupt, KeyQuit:
			m.done = true
			return m, tea.Quit
		case KeySelect:
			selected := m.list.SelectedItem()
			if item, ok := selected.(CommitItem); ok {
				if item.provider == ProviderManual {
					m.manualMode = true
					m.manualInput = ""
				} else {
					m.finalChoice = item.message
					m.done = true
					return m, tea.Quit
				}
			}
			return m, nil
		default:
			for checkboxID, checkboxKey := range checkboxKeymaps {
				if msg.String() == checkboxKey {
					if _, exists := m.checkboxes[checkboxID]; !exists {
						return m, nil // Ignore unknown checkbox IDs
					}

					// Check if dry-run is enabled and prevent toggling other checkboxes
					if m.checkboxes[CheckboxIDDryRun] && checkboxID != CheckboxIDDryRun {
						return m, nil // Don't allow toggling when dry-run is active
					}

					// Handle mutually exclusive tag checkboxes
					if IsTagCheckbox(checkboxID) {
						// Store current state before clearing
						wasChecked := m.checkboxes[checkboxID]

						// Clear all tag checkboxes
						m.checkboxes[CheckboxIDCreateTagMajor] = false
						m.checkboxes[CheckboxIDCreateTagMinor] = false
						m.checkboxes[CheckboxIDCreateTagPatch] = false

						// Toggle the selected one (allow unchecking)
						m.checkboxes[checkboxID] = !wasChecked
					} else if checkboxID == CheckboxIDDryRun {
						// Toggle dry-run
						m.checkboxes[checkboxID] = !m.checkboxes[checkboxID]

						// If enabling dry-run, disable all other checkboxes
						if m.checkboxes[CheckboxIDDryRun] {
							for id := range m.checkboxes {
								if id != CheckboxIDDryRun {
									m.checkboxes[id] = false
								}
							}
						}
					} else {
						// Normal toggle for other checkboxes
						m.checkboxes[checkboxID] = !m.checkboxes[checkboxID]
					}

					return m, nil
				}
			}
		}
	}

	// Update the list if we're not in manual mode
	if !m.manualMode {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}

	return m, nil
}

// updateManualMode handles input in manual entry mode
func (m Model) updateManualMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case KeyInterrupt:
		m.done = true
		return m, tea.Quit
	case KeyCancel:
		m.manualMode = false
		m.manualInput = ""
	case KeyNewLine:
		// Enter for new lines
		m.manualInput += "\n"
	case KeyFinishInput:
		// Ctrl+D to finish multi-line input
		trimmed := strings.TrimSpace(m.manualInput)
		if trimmed != "" && len(trimmed) >= 3 { // Require at least 3 characters
			m.finalChoice = trimmed
			m.done = true
			return m, tea.Quit
		}
		// Ignore if input is too short
	case KeyBackspace:
		if len(m.manualInput) > 0 {
			runes := []rune(m.manualInput)
			if len(runes) > 0 {
				m.manualInput = string(runes[:len(runes)-1])
			}
		}
	case KeySpace:
		m.manualInput += " "
	default:
		if msg.Type == tea.KeyRunes {
			m.manualInput += string(msg.Runes)
		}
	}
	return m, nil
}

// View renders the UI
func (m Model) View() string {
	if m.done {
		return ""
	}

	paddedStyle := lipgloss.NewStyle().
		Padding(PaddingTop, PaddingHorizontal)

	if m.manualMode {
		return paddedStyle.Render(m.renderManualMode())
	}

	return paddedStyle.Render(
		lipgloss.JoinVertical(
			lipgloss.Left,
			m.list.View(),
			m.renderFooter(),
		),
	)
}

// renderFooter renders the checkbox footer
func (m Model) renderFooter() string {
	// Calculate available width for footer
	availableWidth := m.width - (PaddingHorizontal * 2)
	if availableWidth < 40 {
		availableWidth = 40 // Minimum width
	}

	footerStyle := lipgloss.NewStyle().
		BorderTop(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(ColorBorder)).
		MarginTop(0).
		PaddingTop(0).
		PaddingBottom(0).
		Width(availableWidth)

	var checkboxes []string
	for _, opt := range footerCheckboxes {
		// Determine checkbox symbol based on type
		var checkbox string
		var boxStyle lipgloss.Style

		isDryRunActive := m.checkboxes[CheckboxIDDryRun] && opt.id != CheckboxIDDryRun

		if IsTagCheckbox(opt.id) {
			// Use radio buttons for mutually exclusive tag options
			if m.checkboxes[opt.id] {
				checkbox = "●" // Filled radio button
				boxStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color(ColorPrimary))
			} else {
				checkbox = "○" // Empty radio button
				if isDryRunActive {
					boxStyle = lipgloss.NewStyle().
						Foreground(lipgloss.Color(ColorDimmedDarker))
				} else {
					boxStyle = lipgloss.NewStyle().
						Foreground(lipgloss.Color(ColorMuted))
				}
			}
		} else {
			// Use checkboxes for regular options
			if m.checkboxes[opt.id] {
				checkbox = CheckboxChecked
				boxStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color(ColorPrimary))
			} else {
				checkbox = CheckboxUnchecked
				if isDryRunActive {
					boxStyle = lipgloss.NewStyle().
						Foreground(lipgloss.Color(ColorDimmedDarker))
				} else {
					boxStyle = lipgloss.NewStyle().
						Foreground(lipgloss.Color(ColorMuted))
				}
			}
		}

		// Style for the label
		labelStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorNormal))

		if isDryRunActive {
			// Dim disabled items
			labelStyle = labelStyle.
				Foreground(lipgloss.Color(ColorDimmedDark))
		} else if m.checkboxes[opt.id] {
			labelStyle = labelStyle.
				Foreground(lipgloss.Color(ColorPrimary)).
				Bold(true)
		}

		// Key number style
		keyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorDimmedDark))

		if isDryRunActive {
			keyStyle = keyStyle.
				Foreground(lipgloss.Color(ColorDimmedDarker))
		}

		// Format: 1 ▢ Label
		item := keyStyle.Render(opt.key) + " " +
			boxStyle.Render(checkbox) + " " +
			labelStyle.Render(opt.label)

		checkboxes = append(checkboxes, item)
	}

	// Join checkboxes horizontally with spacing
	parts := make([]string, 0, max(0, len(checkboxes)*2-1))
	for i, cb := range checkboxes {
		if i > 0 {
			parts = append(parts, "  ")
		}
		parts = append(parts, cb)
	}
	checkboxLine := lipgloss.JoinHorizontal(lipgloss.Top, parts...)

	// Help text
	helpStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorDimmedDark)).
		Italic(true).
		MarginTop(1)

	helpText := helpStyle.Render(FooterHelp)

	// Combine checkbox line and help
	content := lipgloss.JoinVertical(lipgloss.Left, checkboxLine, helpText)

	return footerStyle.Render(content)
}

// renderManualMode renders the manual input screen with fancy styling
func (m Model) renderManualMode() string {
	// Use same title style as main list for consistency
	titleStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(ColorAccent)).
		Foreground(lipgloss.Color(ColorBright)).
		Bold(true).
		Italic(true).
		Padding(0, 2).
		MarginBottom(1).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ColorAccent)).
		BorderBottom(true)

	// Calculate input width accounting for:
	// - Horizontal padding (PaddingHorizontal * 2)
	// - Border (2 characters)
	// - Internal padding (2 characters)
	maxInputWidth := m.width - (PaddingHorizontal * 2) - 4
	inputWidth := min(ManualInputWidth, maxInputWidth)
	if inputWidth < 20 {
		inputWidth = 20 // Minimum usable width
	}

	inputStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ColorAccent)).
		Padding(1).
		Width(inputWidth).
		Height(ManualInputHeight)

	helpStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorMuted)).
		MarginTop(1)

	var b strings.Builder

	b.WriteString(titleStyle.Render(ManualInputTitle))
	b.WriteString("\n\n")

	// Show the input with cursor at the correct position
	input := m.manualInput
	if input == "" {
		// Empty input - just show cursor
		input = Cursor
	} else {
		// Add cursor at the end of input
		lines := strings.Split(input, "\n")
		if len(lines) > 0 {
			lines[len(lines)-1] += Cursor
		}
		input = strings.Join(lines, "\n")
	}

	b.WriteString(inputStyle.Render(input))
	b.WriteString("\n")

	// Show validation hint if input is too short
	trimmed := strings.TrimSpace(m.manualInput)
	if trimmed != "" && len(trimmed) < minCommitMessageLength {
		warningStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorWarning)).
			Italic(true)
		b.WriteString(
			warningStyle.Render(fmt.Sprintf("Message must be at least %d characters", minCommitMessageLength)),
		)
		b.WriteString("\n")
	}

	b.WriteString(helpStyle.Render(ManualInputHelp))

	return b.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// GetFinalChoice returns the selected commit message
func (m Model) GetFinalChoice() string {
	return m.finalChoice
}

// IsDone returns whether the user has made a selection
func (m Model) IsDone() bool {
	return m.done
}

func (m Model) GetCheckboxValue(id string) bool {
	if value, exists := m.checkboxes[id]; exists {
		return value
	}
	return false
}
