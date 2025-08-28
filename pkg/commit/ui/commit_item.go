package ui

import (
	"strings"
)

// CommitItem represents a commit message suggestion as a list item
type CommitItem struct {
	provider string
	message  string
	lines    []string
}

// Title returns the title of the item (provider name)
func (i CommitItem) Title() string {
	if i.provider == ProviderManual {
		return ManualOptionTitle
	}
	return strings.ToTitle(i.provider)
}

// Description returns the description (shows all lines for multi-line messages)
func (i CommitItem) Description() string {
	if i.provider == ProviderManual {
		return ManualOptionDesc
	}
	// For multi-line messages, join with line breaks
	if len(i.lines) > 1 {
		// Return up to 5 lines for preview
		preview := i.lines
		if len(preview) > 5 {
			preview = append(preview[:5], "...")
		}
		return strings.Join(preview, "\n")
	}
	if len(i.lines) > 0 {
		return i.lines[0]
	}
	return i.message
}

// FilterValue returns the value to filter on
func (i CommitItem) FilterValue() string {
	return i.provider + " " + i.message
}
