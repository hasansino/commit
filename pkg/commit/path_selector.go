package commit

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// pathSelectorMatcher matches the positive, repository-relative selectors
// accepted by --include-only and --exclude. It intentionally does not
// implement gitignore precedence or negation; native Git owns ignore rules.
type pathSelectorMatcher struct {
	patterns []pathSelectorPattern
}

type pathSelectorPattern struct {
	components    []string
	rootRelative  bool
	directoryOnly bool
}

func newPathSelectorMatcher(kind string, patterns []string) (*pathSelectorMatcher, error) {
	if len(patterns) == 0 {
		return nil, nil
	}

	matcher := &pathSelectorMatcher{
		patterns: make([]pathSelectorPattern, 0, len(patterns)),
	}
	for _, pattern := range patterns {
		if strings.HasPrefix(pattern, "!") {
			return nil, fmt.Errorf(
				"invalid %s pattern %q: include and exclude patterns are positive selectors and do not support negation",
				kind,
				pattern,
			)
		}

		compiled, ok := compilePathSelectorPattern(pattern)
		if ok {
			matcher.patterns = append(matcher.patterns, compiled)
		}
	}

	return matcher, nil
}

func compilePathSelectorPattern(pattern string) (pathSelectorPattern, bool) {
	// Preserve the existing selector behavior inherited from gitignore syntax:
	// unescaped trailing spaces are insignificant, while "\\ " is literal.
	if !strings.HasSuffix(pattern, `\ `) {
		pattern = strings.TrimRight(pattern, " ")
	}
	if pattern == "" {
		return pathSelectorPattern{}, false
	}

	directoryOnly := strings.HasSuffix(pattern, "/")
	if directoryOnly {
		pattern = strings.TrimSuffix(pattern, "/")
	}
	if pattern == "" {
		return pathSelectorPattern{}, false
	}

	// A slashless selector floats across complete path components. A trailing
	// slash alone does not root a selector, so "build/" finds a build directory
	// at any depth. Any other slash makes the selector repository-root-relative.
	rootRelative := strings.Contains(pattern, "/")
	if rootRelative {
		pattern = strings.TrimPrefix(pattern, "/")
	}

	return pathSelectorPattern{
		components:    strings.Split(pattern, "/"),
		rootRelative:  rootRelative,
		directoryOnly: directoryOnly,
	}, true
}

func (m *pathSelectorMatcher) Match(candidate string) bool {
	if m == nil {
		return false
	}

	components := splitPathSelectorCandidate(candidate)
	if len(components) == 0 {
		return false
	}
	for _, pattern := range m.patterns {
		if pattern.match(components) {
			return true
		}
	}
	return false
}

func splitPathSelectorCandidate(candidate string) []string {
	candidate = filepath.ToSlash(candidate)
	candidate = strings.TrimPrefix(candidate, "./")
	candidate = strings.Trim(candidate, "/")
	if candidate == "" {
		return nil
	}
	return strings.Split(candidate, "/")
}

func (p pathSelectorPattern) match(candidate []string) bool {
	if !p.rootRelative {
		return p.matchFloatingComponent(candidate)
	}
	return matchRootRelativeSelector(p.components, candidate, p.directoryOnly)
}

func (p pathSelectorPattern) matchFloatingComponent(candidate []string) bool {
	pattern := p.components[0]
	for index, component := range candidate {
		matched, err := path.Match(pattern, component)
		if err != nil || !matched {
			continue
		}
		if !p.directoryOnly || index < len(candidate)-1 {
			return true
		}
	}
	return false
}

func matchRootRelativeSelector(pattern, candidate []string, directoryOnly bool) bool {
	type matchState struct {
		patternIndex   int
		candidateIndex int
	}
	memo := make(map[matchState]bool)
	visited := make(map[matchState]bool)

	var match func(patternIndex, candidateIndex int) bool
	match = func(patternIndex, candidateIndex int) bool {
		state := matchState{patternIndex: patternIndex, candidateIndex: candidateIndex}
		if visited[state] {
			return memo[state]
		}
		visited[state] = true
		defer func() {
			// Individual branches store successful results before returning. A
			// state that reaches this defer without doing so is a failed match.
			if _, ok := memo[state]; !ok {
				memo[state] = false
			}
		}()

		if patternIndex == len(pattern) {
			// Selectors also match descendants of a matched directory. A trailing
			// slash requires such a descendant rather than matching a file itself.
			memo[state] = !directoryOnly || candidateIndex < len(candidate)
			return memo[state]
		}

		componentPattern := pattern[patternIndex]
		if componentPattern == "**" {
			if patternIndex == len(pattern)-1 {
				// Git-style trailing /** means everything inside the directory,
				// but not the directory itself.
				memo[state] = candidateIndex < len(candidate)
				return memo[state]
			}
			for next := candidateIndex; next <= len(candidate); next++ {
				if match(patternIndex+1, next) {
					memo[state] = true
					return true
				}
			}
			return false
		}

		if candidateIndex == len(candidate) {
			return false
		}
		matched, err := path.Match(componentPattern, candidate[candidateIndex])
		if err != nil || !matched {
			return false
		}
		memo[state] = match(patternIndex+1, candidateIndex+1)
		return memo[state]
	}

	return match(0, 0)
}
