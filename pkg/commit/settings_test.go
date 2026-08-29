package commit

import (
	"strings"
	"testing"
	"time"
)

func TestSettingsValidateJiraOptions(t *testing.T) {
	t.Run("valid positions", func(t *testing.T) {
		for _, position := range []string{"", "none", "prefix", "infix", "suffix", "PREFIX"} {
			settings := &Settings{
				Timeout:          time.Second,
				JiraTaskPosition: position,
			}
			if err := settings.Validate(); err != nil {
				t.Errorf("position %q: Validate() error = %v", position, err)
			}
		}
	})

	t.Run("valid styles", func(t *testing.T) {
		for _, style := range []string{
			"", "none", "plain", "plain-colon", "brackets", "parens", "BRACKETS",
		} {
			settings := &Settings{
				Timeout:       time.Second,
				JiraTaskStyle: style,
			}
			if err := settings.Validate(); err != nil {
				t.Errorf("style %q: Validate() error = %v", style, err)
			}
		}
	})

	tests := []struct {
		name            string
		position        string
		style           string
		wantErrContains string
	}{
		{
			name:            "invalid position",
			position:        "middle",
			wantErrContains: "invalid jira task position: middle",
		},
		{
			name:            "position whitespace is not ignored",
			position:        " prefix ",
			wantErrContains: "invalid jira task position:  prefix ",
		},
		{
			name:            "invalid style",
			style:           "bracket",
			wantErrContains: "invalid jira task style: bracket",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := &Settings{
				Timeout:          time.Second,
				JiraTaskPosition: tt.position,
				JiraTaskStyle:    tt.style,
			}
			err := settings.Validate()
			if err == nil {
				t.Fatal("Validate() error = nil, want an error")
			}
			if !strings.Contains(err.Error(), tt.wantErrContains) {
				t.Errorf("Validate() error = %q, want it to contain %q", err, tt.wantErrContains)
			}
		})
	}
}
