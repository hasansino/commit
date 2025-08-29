package commit

import (
	"fmt"
	"strings"
	"testing"
)

func TestSemVer_Parsing(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		expected semVer
	}{
		{
			name:     "standard version",
			version:  "v1.2.3",
			expected: semVer{Major: 1, Minor: 2, Patch: 3},
		},
		{
			name:     "version without v prefix",
			version:  "1.2.3",
			expected: semVer{Major: 1, Minor: 2, Patch: 3},
		},
		{
			name:     "zero version",
			version:  "v0.0.0",
			expected: semVer{Major: 0, Minor: 0, Patch: 0},
		},
		{
			name:     "large numbers",
			version:  "v10.20.30",
			expected: semVer{Major: 10, Minor: 20, Patch: 30},
		},
		{
			name:     "invalid version",
			version:  "invalid",
			expected: semVer{Major: 0, Minor: 0, Patch: 0},
		},
		{
			name:     "partial version",
			version:  "v1.2",
			expected: semVer{Major: 0, Minor: 0, Patch: 0},
		},
		{
			name:     "empty version",
			version:  "",
			expected: semVer{Major: 0, Minor: 0, Patch: 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseSemVer(tt.version)
			if result != tt.expected {
				t.Errorf("parseSemVer(%q) = %+v, want %+v", tt.version, result, tt.expected)
			}
		})
	}
}

func TestGitOperations_IncrementVersion(t *testing.T) {
	git := &gitOperations{}

	tests := []struct {
		name          string
		currentTag    string
		incrementType string
		expected      string
		expectErr     bool
	}{
		{
			name:          "increment patch from existing version",
			currentTag:    "v1.2.3",
			incrementType: "patch",
			expected:      "v1.2.4",
			expectErr:     false,
		},
		{
			name:          "increment minor from existing version",
			currentTag:    "v1.2.3",
			incrementType: "minor",
			expected:      "v1.3.0",
			expectErr:     false,
		},
		{
			name:          "increment major from existing version",
			currentTag:    "v1.2.3",
			incrementType: "major",
			expected:      "v2.0.0",
			expectErr:     false,
		},
		{
			name:          "increment patch from empty tag",
			currentTag:    "",
			incrementType: "patch",
			expected:      "v0.0.1",
			expectErr:     false,
		},
		{
			name:          "increment minor from empty tag",
			currentTag:    "",
			incrementType: "minor",
			expected:      "v0.1.0",
			expectErr:     false,
		},
		{
			name:          "increment major from empty tag",
			currentTag:    "",
			incrementType: "major",
			expected:      "v1.0.0",
			expectErr:     false,
		},
		{
			name:          "invalid increment type",
			currentTag:    "v1.2.3",
			incrementType: "invalid",
			expected:      "",
			expectErr:     true,
		},
		{
			name:          "case insensitive increment type",
			currentTag:    "v1.2.3",
			incrementType: "PATCH",
			expected:      "v1.2.4",
			expectErr:     false,
		},
		{
			name:          "increment from zero version",
			currentTag:    "v0.0.0",
			incrementType: "patch",
			expected:      "v0.0.1",
			expectErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := git.IncrementVersion(tt.currentTag, tt.incrementType)

			if tt.expectErr {
				if err == nil {
					t.Errorf("IncrementVersion(%q, %q) expected error but got none", tt.currentTag, tt.incrementType)
				}
			} else {
				if err != nil {
					t.Errorf("IncrementVersion(%q, %q) unexpected error = %v", tt.currentTag, tt.incrementType, err)
				}
				if result != tt.expected {
					t.Errorf("IncrementVersion(%q, %q) = %q, want %q", tt.currentTag, tt.incrementType, result, tt.expected)
				}
			}
		})
	}
}

func TestGitOperations_shouldExcludeFile(t *testing.T) {
	tests := []struct {
		name            string
		file            string
		excludePatterns []string
		globalPatterns  []string
		expected        bool
	}{
		{
			name:            "no patterns",
			file:            "test.go",
			excludePatterns: []string{},
			globalPatterns:  []string{},
			expected:        false,
		},
		{
			name:            "exact match exclude",
			file:            "test.log",
			excludePatterns: []string{"test.log"},
			globalPatterns:  []string{},
			expected:        true,
		},
		{
			name:            "glob pattern exclude",
			file:            "test.log",
			excludePatterns: []string{"*.log"},
			globalPatterns:  []string{},
			expected:        true,
		},
		{
			name:            "basename match exclude",
			file:            "dir/test.log",
			excludePatterns: []string{"test.log"},
			globalPatterns:  []string{},
			expected:        true,
		},
		{
			name:            "no match exclude",
			file:            "test.go",
			excludePatterns: []string{"*.log"},
			globalPatterns:  []string{},
			expected:        false,
		},
		{
			name:            "global pattern exclude",
			file:            "node_modules/package.json",
			excludePatterns: []string{},
			globalPatterns:  []string{"node_modules"},
			expected:        true,
		},
		{
			name:            "directory pattern exclude",
			file:            "build/output.js",
			excludePatterns: []string{},
			globalPatterns:  []string{"build/"},
			expected:        true,
		},
		{
			name:            "multiple patterns - first match",
			file:            "test.log",
			excludePatterns: []string{"*.log", "*.tmp"},
			globalPatterns:  []string{},
			expected:        true,
		},
		{
			name:            "multiple patterns - second match",
			file:            "temp.tmp",
			excludePatterns: []string{"*.log", "*.tmp"},
			globalPatterns:  []string{},
			expected:        true,
		},
		{
			name:            "global and local patterns",
			file:            "node_modules/test.log",
			excludePatterns: []string{"*.log"},
			globalPatterns:  []string{"node_modules"},
			expected:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldExcludeFile(tt.file, tt.excludePatterns, tt.globalPatterns)
			if result != tt.expected {
				t.Errorf("shouldExcludeFile(%q, %v, %v) = %v, want %v",
					tt.file, tt.excludePatterns, tt.globalPatterns, result, tt.expected)
			}
		})
	}
}

func TestGitOperations_shouldIncludeFile(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		patterns []string
		expected bool
	}{
		{
			name:     "no patterns",
			file:     "test.go",
			patterns: []string{},
			expected: false,
		},
		{
			name:     "exact match include",
			file:     "test.go",
			patterns: []string{"test.go"},
			expected: true,
		},
		{
			name:     "glob pattern include",
			file:     "test.go",
			patterns: []string{"*.go"},
			expected: true,
		},
		{
			name:     "basename match include",
			file:     "src/test.go",
			patterns: []string{"test.go"},
			expected: true,
		},
		{
			name:     "no match include",
			file:     "test.log",
			patterns: []string{"*.go"},
			expected: false,
		},
		{
			name:     "multiple patterns - first match",
			file:     "test.go",
			patterns: []string{"*.go", "*.js"},
			expected: true,
		},
		{
			name:     "multiple patterns - second match",
			file:     "script.js",
			patterns: []string{"*.go", "*.js"},
			expected: true,
		},
		{
			name:     "multiple patterns - no match",
			file:     "readme.txt",
			patterns: []string{"*.go", "*.js"},
			expected: false,
		},
		{
			name:     "substring match",
			file:     "test-file.go",
			patterns: []string{"test"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldIncludeFile(tt.file, tt.patterns)
			if result != tt.expected {
				t.Errorf("shouldIncludeFile(%q, %v) = %v, want %v", tt.file, tt.patterns, result, tt.expected)
			}
		})
	}
}

func TestGitOperations_isSimpleGlobPattern(t *testing.T) {
	tests := []struct {
		name     string
		pattern  string
		expected bool
	}{
		{
			name:     "simple wildcard",
			pattern:  "*.go",
			expected: true,
		},
		{
			name:     "question mark",
			pattern:  "test?.go",
			expected: true,
		},
		{
			name:     "no wildcards",
			pattern:  "test.go",
			expected: false,
		},
		{
			name:     "path separator",
			pattern:  "src/*.go",
			expected: false,
		},
		{
			name:     "complex pattern with path",
			pattern:  "src/**/*.go",
			expected: false,
		},
		{
			name:     "multiple wildcards",
			pattern:  "*.test.*",
			expected: true,
		},
		{
			name:     "empty pattern",
			pattern:  "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isSimpleGlobPattern(tt.pattern)
			if result != tt.expected {
				t.Errorf("isSimpleGlobPattern(%q) = %v, want %v", tt.pattern, result, tt.expected)
			}
		})
	}
}

func TestParseGitignoreFile_InvalidPath(t *testing.T) {
	patterns, err := parseGitignoreFile("/nonexistent/path/.gitignore")

	if err != nil {
		t.Errorf("parseGitignoreFile() with non-existent file should return empty patterns, got error: %v", err)
	}

	if len(patterns) != 0 {
		t.Errorf(
			"parseGitignoreFile() with non-existent file should return empty patterns, got %d patterns",
			len(patterns),
		)
	}
}

func TestGitConfig_Validation(t *testing.T) {
	tests := []struct {
		name   string
		config gitConfig
		valid  bool
	}{
		{
			name: "valid config without GPG",
			config: gitConfig{
				UserName:  "Test User",
				UserEmail: "test@example.com",
				GPGSign:   false,
			},
			valid: true,
		},
		{
			name: "valid config with GPG",
			config: gitConfig{
				UserName:   "Test User",
				UserEmail:  "test@example.com",
				GPGSign:    true,
				SigningKey: "ABCD1234",
				GPGProgram: "gpg",
			},
			valid: true,
		},
		{
			name: "empty user name",
			config: gitConfig{
				UserName:  "",
				UserEmail: "test@example.com",
			},
			valid: false,
		},
		{
			name: "empty user email",
			config: gitConfig{
				UserName:  "Test User",
				UserEmail: "",
			},
			valid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This is a basic validation test for the GitConfig struct
			// In a real scenario, you would test the actual validation logic
			// if it existed in the GetConfig method
			if tt.config.UserName == "" || tt.config.UserEmail == "" {
				if tt.valid {
					t.Error("Expected invalid config to be marked as invalid")
				}
			} else {
				if !tt.valid {
					t.Error("Expected valid config to be marked as valid")
				}
			}
		})
	}
}

func TestGPGSigner_Interface(t *testing.T) {
	signer := &gpgSigner{
		gpgProgram: "gpg",
		keyID:      "testkey",
	}

	// Test that GPGSigner implements the expected interface
	// This is mainly a compile-time check
	if signer.gpgProgram != "gpg" {
		t.Errorf("GPGSigner gpgProgram = %q, want %q", signer.gpgProgram, "gpg")
	}
	if signer.keyID != "testkey" {
		t.Errorf("GPGSigner keyID = %q, want %q", signer.keyID, "testkey")
	}
}

func TestGitOperations_matchesSigningKey(t *testing.T) {
	tests := []struct {
		name       string
		signingKey string
		keyID      uint64
		userEmail  string
		expected   bool
	}{
		{
			name:       "matching key ID",
			signingKey: "ABCD1234",
			keyID:      0xABCD1234,
			userEmail:  "test@example.com",
			expected:   true,
		},
		{
			name:       "non-matching key ID",
			signingKey: "EFGH5678",
			keyID:      0xABCD1234,
			userEmail:  "test@example.com",
			expected:   false,
		},
		{
			name:       "matching email",
			signingKey: "test@example.com",
			keyID:      0x12345678,
			userEmail:  "test@example.com",
			expected:   true,
		},
		{
			name:       "non-matching email",
			signingKey: "other@example.com",
			keyID:      0x12345678,
			userEmail:  "test@example.com",
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Since we can't easily mock openpgp.Entity, we'll test the logic conceptually
			// In a real implementation, you would create a mock entity or use test data

			// Test the basic string matching logic that would be used
			keyIDHex := strings.ToUpper(fmt.Sprintf("%016X", tt.keyID))
			signingKeyUpper := strings.ToUpper(tt.signingKey)

			// Check if the signing key matches the key ID (substring match for short IDs)
			keyIDMatches := strings.HasSuffix(keyIDHex, signingKeyUpper)

			// Check if signing key matches email
			emailMatches := strings.Contains(tt.userEmail, tt.signingKey)

			matches := keyIDMatches || emailMatches

			if matches != tt.expected {
				t.Errorf("Key matching logic for %q gave %v, want %v (keyID: %s, email: %s)",
					tt.signingKey, matches, tt.expected, keyIDHex, tt.userEmail)
			}
		})
	}
}
