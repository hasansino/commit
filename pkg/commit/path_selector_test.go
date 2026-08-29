package commit

import (
	"strings"
	"testing"
)

func TestPathSelectorMatcher(t *testing.T) {
	tests := []struct {
		name      string
		patterns  []string
		candidate string
		wantMatch bool
	}{
		{
			name:      "empty pattern does not match",
			patterns:  []string{""},
			candidate: "main.go",
		},
		{
			name:      "literal matches root file",
			patterns:  []string{"api.go"},
			candidate: "api.go",
			wantMatch: true,
		},
		{
			name:      "literal matches complete nested basename",
			patterns:  []string{"api.go"},
			candidate: "src/api.go",
			wantMatch: true,
		},
		{
			name:      "literal matches complete directory component",
			patterns:  []string{"api"},
			candidate: "src/api/handler.go",
			wantMatch: true,
		},
		{
			name:      "literal is not an implicit substring",
			patterns:  []string{"api"},
			candidate: "rapid.go",
		},
		{
			name:      "explicit wildcard enables substring matching",
			patterns:  []string{"*api*"},
			candidate: "rapid.go",
			wantMatch: true,
		},
		{
			name:      "slashless extension glob floats to nested basename",
			patterns:  []string{"*.go"},
			candidate: "src/internal/main.go",
			wantMatch: true,
		},
		{
			name:      "question mark is explicit wildcard",
			patterns:  []string{"test?.go"},
			candidate: "nested/test1.go",
			wantMatch: true,
		},
		{
			name:      "character class is explicit wildcard",
			patterns:  []string{"[ab]pi.go"},
			candidate: "api.go",
			wantMatch: true,
		},
		{
			name:      "slash pattern is repository root relative",
			patterns:  []string{"src/*.go"},
			candidate: "src/main.go",
			wantMatch: true,
		},
		{
			name:      "slash pattern does not float",
			patterns:  []string{"src/*.go"},
			candidate: "nested/src/main.go",
		},
		{
			name:      "single star does not cross a component",
			patterns:  []string{"src/*.go"},
			candidate: "src/nested/main.go",
		},
		{
			name:      "root path selecting directory includes descendants",
			patterns:  []string{"src/api"},
			candidate: "src/api/handler.go",
			wantMatch: true,
		},
		{
			name:      "slashless trailing slash matches directory descendants",
			patterns:  []string{"build/"},
			candidate: "nested/build/output.js",
			wantMatch: true,
		},
		{
			name:      "trailing slash respects complete component",
			patterns:  []string{"build/"},
			candidate: "nested/rebuild/output.js",
		},
		{
			name:      "trailing slash does not match a file of that name",
			patterns:  []string{"build/"},
			candidate: "nested/build",
		},
		{
			name:      "multi-component trailing slash is root relative",
			patterns:  []string{"src/generated/"},
			candidate: "src/generated/output.txt",
			wantMatch: true,
		},
		{
			name:      "multi-component trailing slash does not float",
			patterns:  []string{"src/generated/"},
			candidate: "nested/src/generated/output.txt",
		},
		{
			name:      "double star matches zero directories",
			patterns:  []string{"src/**/main.go"},
			candidate: "src/main.go",
			wantMatch: true,
		},
		{
			name:      "double star matches multiple directories",
			patterns:  []string{"src/**/main.go"},
			candidate: "src/a/b/main.go",
			wantMatch: true,
		},
		{
			name:      "leading double star matches root file",
			patterns:  []string{"**/*.go"},
			candidate: "main.go",
			wantMatch: true,
		},
		{
			name:      "leading double star matches nested file",
			patterns:  []string{"**/*.go"},
			candidate: "src/internal/main.go",
			wantMatch: true,
		},
		{
			name:      "trailing double star matches direct descendant",
			patterns:  []string{"generated/**"},
			candidate: "generated/file.txt",
			wantMatch: true,
		},
		{
			name:      "trailing double star matches deep descendant",
			patterns:  []string{"generated/**"},
			candidate: "generated/a/b/file.txt",
			wantMatch: true,
		},
		{
			name:      "trailing double star does not match base path",
			patterns:  []string{"generated/**"},
			candidate: "generated",
		},
		{
			name:      "multiple double stars match without combinatorial ambiguity",
			patterns:  []string{"src/**/**/main.go"},
			candidate: "src/a/b/c/main.go",
			wantMatch: true,
		},
		{
			name:      "unescaped trailing spaces are ignored",
			patterns:  []string{"generated/**   "},
			candidate: "generated/file.txt",
			wantMatch: true,
		},
		{
			name:      "multiple patterns use positive OR semantics",
			patterns:  []string{"*.go", "scripts/*.js"},
			candidate: "scripts/release.js",
			wantMatch: true,
		},
		{
			name:      "multiple patterns can all miss",
			patterns:  []string{"*.go", "scripts/*.js"},
			candidate: "README.md",
		},
		{
			name:      "malformed character class fails closed",
			patterns:  []string{"[abc"},
			candidate: "a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matcher, err := newPathSelectorMatcher("include-only", tt.patterns)
			if err != nil {
				t.Fatalf("newPathSelectorMatcher() error = %v", err)
			}
			if got := matcher.Match(tt.candidate); got != tt.wantMatch {
				t.Errorf(
					"Match(%q, %q) = %v, want %v",
					tt.patterns,
					tt.candidate,
					got,
					tt.wantMatch,
				)
			}
		})
	}
}

func TestPathSelectorMatcher_NoPatterns(t *testing.T) {
	matcher, err := newPathSelectorMatcher("exclude", nil)
	if err != nil {
		t.Fatalf("newPathSelectorMatcher() error = %v", err)
	}
	if matcher != nil {
		t.Fatalf("newPathSelectorMatcher(nil) = %#v, want nil", matcher)
	}
	if matcher.Match("anything.txt") {
		t.Fatal("nil pathSelectorMatcher unexpectedly matched")
	}
}

func TestPathSelectorMatcher_RejectsNegation(t *testing.T) {
	for _, kind := range []string{"include-only", "exclude"} {
		t.Run(kind, func(t *testing.T) {
			matcher, err := newPathSelectorMatcher(kind, []string{"*.go", "!generated.go"})
			if err == nil {
				t.Fatalf("newPathSelectorMatcher() = (%#v, nil), want negation error", matcher)
			}
			if !strings.Contains(err.Error(), kind) ||
				!strings.Contains(err.Error(), "positive selectors") {
				t.Fatalf("newPathSelectorMatcher() error = %q, want kind and positive-selector guidance", err)
			}
		})
	}
}

func TestPathSelectorMatcher_AllowsEscapedLeadingBang(t *testing.T) {
	matcher, err := newPathSelectorMatcher("include-only", []string{`\!important.txt`})
	if err != nil {
		t.Fatalf("newPathSelectorMatcher() error = %v", err)
	}
	if !matcher.Match("nested/!important.txt") {
		t.Fatal("escaped leading bang did not match the literal filename")
	}
}
