package commit

import (
	"testing"
	"time"
)

func TestSettings_Validate(t *testing.T) {
	tests := []struct {
		name     string
		settings *Settings
		wantErr  bool
		errMsg   string
	}{
		{
			name: "valid settings",
			settings: &Settings{
				Providers:          []string{"openai"},
				Timeout:            30 * time.Second,
				CustomPrompt:       "custom prompt",
				First:              false,
				Auto:               false,
				DryRun:             false,
				ExcludePatterns:    []string{"*.log"},
				IncludePatterns:    []string{"*.go"},
				Modules:            []string{"jiraPrefixDetector"},
				MultiLine:          false,
				Push:               false,
				Tag:                "patch",
				UseGlobalGitignore: false,
			},
			wantErr: false,
		},
		{
			name:     "nil settings",
			settings: nil,
			wantErr:  true,
			errMsg:   "options cannot be nil",
		},
		{
			name: "zero timeout",
			settings: &Settings{
				Timeout: 0,
			},
			wantErr: true,
			errMsg:  "timeout must be greater than zero",
		},
		{
			name: "negative timeout",
			settings: &Settings{
				Timeout: -5 * time.Second,
			},
			wantErr: true,
			errMsg:  "timeout must be greater than zero",
		},
		{
			name: "valid tag - major",
			settings: &Settings{
				Timeout: 30 * time.Second,
				Tag:     "major",
			},
			wantErr: false,
		},
		{
			name: "valid tag - minor",
			settings: &Settings{
				Timeout: 30 * time.Second,
				Tag:     "minor",
			},
			wantErr: false,
		},
		{
			name: "valid tag - patch",
			settings: &Settings{
				Timeout: 30 * time.Second,
				Tag:     "patch",
			},
			wantErr: false,
		},
		{
			name: "empty tag",
			settings: &Settings{
				Timeout: 30 * time.Second,
				Tag:     "",
			},
			wantErr: false,
		},
		{
			name: "invalid tag",
			settings: &Settings{
				Timeout: 30 * time.Second,
				Tag:     "invalid",
			},
			wantErr: true,
			errMsg:  "invalid tag increment type: invalid (must be major, minor, or patch)",
		},
		{
			name: "minimal valid settings",
			settings: &Settings{
				Timeout: 1 * time.Nanosecond,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.settings.Validate()

			if tt.wantErr {
				if err == nil {
					t.Errorf("Settings.Validate() expected error but got none")
					return
				}
				if tt.errMsg != "" && err.Error() != tt.errMsg {
					t.Errorf("Settings.Validate() error = %q, want %q", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("Settings.Validate() unexpected error = %v", err)
				}
			}
		})
	}
}
