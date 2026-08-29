package commit

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type gitOperations struct {
	repo         *git.Repository
	repoPath     string
	sessionLease sync.Mutex
}

type gitConfig struct {
	UserName   string
	UserEmail  string
	GPGSign    bool
	SigningKey string
	GPGProgram string
}

// semVer represents a semantic version
type semVer struct {
	Major int
	Minor int
	Patch int
}

func newGitOperations(repoPath string) (*gitOperations, error) {
	if err := validateGitRoutingEnvironment(); err != nil {
		return nil, err
	}

	absRepoPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve repository path: %w", err)
	}

	repo, err := git.PlainOpenWithOptions(repoPath, &git.PlainOpenOptions{
		DetectDotGit:          true,
		EnableDotGitCommonDir: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open git repository: %w", err)
	}
	worktree, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("failed to get repository worktree: %w", err)
	}
	worktreeRoot := worktree.Filesystem.Root()
	if worktreeRoot == "" {
		worktreeRoot = absRepoPath
	}
	worktreeRoot, err = filepath.Abs(worktreeRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve repository worktree: %w", err)
	}

	return &gitOperations{repo: repo, repoPath: worktreeRoot}, nil
}

func (g *gitOperations) gitCommand(args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Env = sanitizedGitEnvironment(os.Environ())
	if g.repoPath != "" {
		cmd.Dir = g.repoPath
	}
	return cmd
}

var gitRoutingEnvironment = map[string]struct{}{
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": {},
	"GIT_COMMON_DIR":                   {},
	"GIT_DIR":                          {},
	"GIT_INDEX_FILE":                   {},
	"GIT_NAMESPACE":                    {},
	"GIT_OBJECT_DIRECTORY":             {},
	"GIT_WORK_TREE":                    {},
}

func validateGitRoutingEnvironment() error {
	for variable := range gitRoutingEnvironment {
		if value, ok := os.LookupEnv(variable); ok && value != "" {
			return fmt.Errorf("%s is not supported; unset it before running commit", variable)
		}
	}
	return nil
}

func sanitizedGitEnvironment(environment []string) []string {
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if _, routed := gitRoutingEnvironment[name]; !routed {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

// GetConfig reads git configuration - fails if user.name or user.email not configured
func (g *gitOperations) GetConfig() (*gitConfig, error) {
	config := &gitConfig{
		GPGSign:    false,
		GPGProgram: "gpg",
	}

	// Get required user configuration
	userName := g.getConfigValue("user.name")
	if userName == "" {
		return nil, fmt.Errorf("git user.name not configured. Run: git config user.name \"Your Name\"")
	}
	config.UserName = userName

	userEmail := g.getConfigValue("user.email")
	if userEmail == "" {
		return nil, fmt.Errorf("git user.email not configured. Run: git config user.email \"your.email@example.com\"")
	}
	config.UserEmail = userEmail

	// Read optional GPG configuration
	if gpgSign := g.getConfigValue("commit.gpgsign"); gpgSign != "" {
		config.GPGSign = strings.ToLower(gpgSign) == "true"
	}
	if signingKey := g.getConfigValue("user.signingkey"); signingKey != "" {
		config.SigningKey = signingKey
	}
	if gpgProgram := g.getConfigValue("gpg.program"); gpgProgram != "" {
		config.GPGProgram = gpgProgram
	}

	return config, nil
}

// getConfigValue reads a specific git config value using git command
func (g *gitOperations) getConfigValue(key string) string {
	cmd := g.gitCommand("config", key)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// getGlobalGitignoreFile reads core.excludesFile from git config and returns the absolute path
func (g *gitOperations) getGlobalGitignoreFile() (string, error) {
	excludesFile := g.getConfigValue("core.excludesFile")
	if excludesFile == "" {
		return "", nil // No global gitignore configured
	}

	// Expand ~ to home directory if needed
	if strings.HasPrefix(excludesFile, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}
		excludesFile = filepath.Join(homeDir, excludesFile[2:])
	}

	// Convert to absolute path if not already
	if !filepath.IsAbs(excludesFile) {
		absPath, err := filepath.Abs(excludesFile)
		if err != nil {
			return "", fmt.Errorf("failed to get absolute path for %s: %w", excludesFile, err)
		}
		excludesFile = absPath
	}

	return excludesFile, nil
}

// parseGitignoreFile returns the file's active patterns in priority order.
func parseGitignoreFile(filePath string) ([]string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil // File doesn't exist, return empty patterns
		}
		return nil, fmt.Errorf("failed to open gitignore file %s: %w", filePath, err)
	}
	defer file.Close()

	var patterns []string
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")

		// Skip empty lines and comments
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}

		patterns = append(patterns, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read gitignore file: %w", err)
	}

	return patterns, nil
}

func (g *gitOperations) GetCurrentBranch() (string, error) {
	head, err := g.snapshotHead()
	if err != nil {
		return "", fmt.Errorf("failed to get HEAD: %w", err)
	}

	branchName := plumbing.ReferenceName(head.name).Short()
	return branchName, nil
}

func (g *gitOperations) GetWorkingTreeStatus() (git.Status, error) {
	worktree, err := g.repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree: %w", err)
	}

	status, err := worktree.Status()
	if err != nil {
		return nil, fmt.Errorf("failed to get status: %w", err)
	}

	return status, nil
}

func (g *gitOperations) UnstageAll() error {
	worktree, err := g.repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	// Single reset operation instead of per-file operations
	err = worktree.Reset(&git.ResetOptions{
		Mode: git.MixedReset,
	})
	if err != nil {
		return fmt.Errorf("failed to reset: %w", err)
	}

	return nil
}

func (g *gitOperations) StageFiles(
	excludePatterns []string,
	includePatterns []string,
	useGlobalGitignore bool,
) ([]string, error) {
	if err := validateSelectorPatterns("exclude", excludePatterns); err != nil {
		return nil, err
	}
	if err := validateSelectorPatterns("include-only", includePatterns); err != nil {
		return nil, err
	}

	worktree, err := g.repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree: %w", err)
	}

	// Load global gitignore patterns if requested
	var globalPatterns []string
	if useGlobalGitignore {
		globalGitignoreFile, err := g.getGlobalGitignoreFile()
		if err != nil {
			return nil, fmt.Errorf("failed to get global gitignore file: %w", err)
		}

		if globalGitignoreFile != "" {
			patterns, err := parseGitignoreFile(globalGitignoreFile)
			if err != nil {
				return nil, fmt.Errorf("failed to parse global gitignore: %w", err)
			}
			globalPatterns = patterns
		}
	}

	// Optimization: if no patterns specified, use AddWithOptions for better performance
	if len(excludePatterns) == 0 && len(includePatterns) == 0 && len(globalPatterns) == 0 {
		return g.stageAllModified(worktree)
	}

	// If we have simple include patterns (glob-compatible) and no global patterns, try to use AddGlob
	if len(excludePatterns) == 0 && len(includePatterns) == 1 && len(globalPatterns) == 0 &&
		isSimpleGlobPattern(includePatterns[0]) {
		return g.stageWithGlob(worktree, includePatterns[0])
	}

	// Fall back to filtered staging for complex patterns
	return g.stageFiltered(worktree, excludePatterns, includePatterns, globalPatterns)
}

// Fast path: stage all modified files
func (g *gitOperations) stageAllModified(worktree *git.Worktree) ([]string, error) {
	// Get status first to return the list of staged files
	status, err := worktree.Status()
	if err != nil {
		return nil, fmt.Errorf("failed to get status: %w", err)
	}

	var modifiedFiles []string
	for file := range status {
		fileStatus := status.File(file)
		if fileStatus.Worktree != git.Unmodified {
			modifiedFiles = append(modifiedFiles, file)
		}
	}

	if len(modifiedFiles) == 0 {
		return []string{}, nil
	}

	// Use AddWithOptions with All flag for better performance
	err = worktree.AddWithOptions(&git.AddOptions{
		All: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to stage all files: %w", err)
	}

	return modifiedFiles, nil
}

// Fast path: use glob patterns when possible
func (g *gitOperations) stageWithGlob(worktree *git.Worktree, pattern string) ([]string, error) {
	// Get status first to return the list of staged files
	status, err := worktree.Status()
	if err != nil {
		return nil, fmt.Errorf("failed to get status: %w", err)
	}

	var matchingFiles []string
	for file := range status {
		fileStatus := status.File(file)
		if fileStatus.Worktree == git.Unmodified {
			continue
		}
		if matched, _ := filepath.Match(pattern, file); matched {
			matchingFiles = append(matchingFiles, file)
		}
	}

	if len(matchingFiles) == 0 {
		return []string{}, nil
	}

	err = worktree.AddGlob(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to stage files with pattern %s: %w", pattern, err)
	}

	return matchingFiles, nil
}

// Fallback: filtered staging for complex patterns
func (g *gitOperations) stageFiltered(
	worktree *git.Worktree,
	excludePatterns, includePatterns []string,
	globalPatterns []string,
) ([]string, error) {
	status, err := worktree.Status()
	if err != nil {
		return nil, fmt.Errorf("failed to get status: %w", err)
	}

	excludeMatcher := newPathPatternMatcher(excludePatterns)
	includeMatcher := newPathPatternMatcher(includePatterns)
	globalMatcher := newPathPatternMatcher(globalPatterns)

	// Build list of files to stage (filtering phase)
	var filesToStage []string
	for file := range status {
		fileStatus := status.File(file)
		if fileStatus.Worktree == git.Unmodified {
			continue
		}

		if shouldExcludeFile(file, excludeMatcher, globalMatcher) {
			continue
		}

		if includeMatcher != nil && !shouldIncludeFile(file, includeMatcher) {
			continue
		}

		filesToStage = append(filesToStage, file)
	}

	// Early return if no files to stage
	if len(filesToStage) == 0 {
		return []string{}, nil
	}

	// Stage files individually (necessary for complex filtering)
	for _, file := range filesToStage {
		_, err := worktree.Add(file)
		if err != nil {
			return nil, fmt.Errorf("failed to stage file %s: %w", file, err)
		}
	}

	return filesToStage, nil
}

// Helper function to check if pattern is simple glob (no complex logic needed)
func isSimpleGlobPattern(pattern string) bool {
	// Simple check: if it contains only *, ?, and regular chars, it's probably a simple glob
	// Exclude patterns with path separators or complex logic
	return !strings.Contains(pattern, "/") &&
		(strings.Contains(pattern, "*") || strings.Contains(pattern, "?"))
}

var contextLevels = []int{5, 3, 2, 1, 0}

func (g *gitOperations) GetStagedDiff(maxSizeBytes int) (string, error) {
	diffFiles, _, err := g.getStagedState()
	if err != nil {
		return "", fmt.Errorf("failed to get staged files: %w", err)
	}

	if len(diffFiles) == 0 {
		return "", nil // No files to diff after filtering
	}

	// Common diff options optimized for AI consumption
	baseDiffOpts := []string{
		"--literal-pathspecs",
		"diff",
		"--cached",
		"--no-color",                // Remove ANSI color codes that confuse AI
		"--no-ext-diff",             // Disable external diff drivers
		"--no-prefix",               // Remove a/ b/ prefixes for cleaner output
		"--diff-algorithm=patience", // Better for code with many similar lines
		"--ignore-space-at-eol",     // Ignore trailing whitespace changes
		"--ignore-cr-at-eol",        // Ignore carriage return differences
		"--function-context",        // Include entire function in diff for better AI understanding
		"--find-renames=50",         // Detect renames with 50% similarity threshold
	}

	// Try different context levels to fit within maxSize
	for _, contextLevel := range contextLevels {
		contextOpts := append([]string{}, baseDiffOpts...)
		contextOpts = append(contextOpts, fmt.Sprintf("-U%d", contextLevel))
		contextOpts = append(contextOpts, "--")
		contextOpts = append(contextOpts, diffFiles...)

		cmd := g.gitCommand(contextOpts...)
		output, err := cmd.Output()
		if err != nil {
			// If the command fails, it might be because no files match - return empty diff
			if strings.Contains(err.Error(), "exit status 128") {
				return "", nil
			}
			return "", fmt.Errorf("failed to get staged diff: %w", err)
		}

		diff := string(output)
		if len(diff) <= maxSizeBytes {
			return diff, nil
		}
	}

	contextOpts := append([]string{}, baseDiffOpts...)
	contextOpts = append(contextOpts, "-U0")
	contextOpts = append(contextOpts, "--")
	contextOpts = append(contextOpts, diffFiles...)

	cmd := g.gitCommand(contextOpts...)
	output, err := cmd.Output()
	if err != nil {
		if strings.Contains(err.Error(), "exit status 128") {
			return "", nil
		}
		return "", fmt.Errorf("failed to get staged diff: %w", err)
	}

	diff := string(output)
	if len(diff) > maxSizeBytes {
		return diff[:maxSizeBytes], nil
	}

	return diff, nil
}

func (g *gitOperations) CreateCommit(
	session *stagingSession,
	message string,
) error {
	if err := g.validateStagingSession(session); err != nil {
		return err
	}
	if session.closed {
		return fmt.Errorf("staging session is already closed")
	}

	// Get git configuration
	config, err := g.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get git config: %w", err)
	}

	worktree, err := g.repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	// Create commit options with real user identity
	commitOptions := &git.CommitOptions{
		Author: &object.Signature{
			Name:  config.UserName,
			Email: config.UserEmail,
			When:  time.Now(),
		},
	}

	// Add GPG signing if enabled
	if config.GPGSign {
		if config.SigningKey == "" {
			return fmt.Errorf("commit.gpgsign=true but user.signingkey not configured")
		}

		// First try to use gpg-agent if available (preferred method)
		if g.isGPGAgentAvailable(config.GPGProgram) {
			signer, err := g.createGPGSigner(config)
			if err != nil {
				return fmt.Errorf("failed to create GPG signer %s: %w", config.SigningKey, err)
			}
			commitOptions.Signer = signer
		} else {
			// Fallback to direct keyring access with manual passphrase
			signKey, err := g.loadKeyDirectly(config)
			if err != nil {
				return fmt.Errorf("failed to load GPG signing key %s: %w", config.SigningKey, err)
			}
			commitOptions.SignKey = signKey
		}
	}

	releaseLocks, err := g.lockAndVerifyStaging(session)
	if err != nil {
		return err
	}
	released := false
	defer func() {
		if !released {
			_ = releaseLocks()
		}
	}()

	commitHash, err := worktree.Commit(message, commitOptions)
	if err != nil {
		commitCreated := false
		var outcomeErr error
		if !commitHash.IsZero() {
			currentHead, headErr := g.snapshotHead()
			if headErr != nil {
				// A non-zero hash means the commit object was written and the
				// HEAD update was attempted. If its outcome cannot be read, do
				// not risk rolling the index back across a successful commit.
				commitCreated = true
				outcomeErr = fmt.Errorf("failed to verify HEAD after commit error: %w", headErr)
			} else {
				commitCreated = currentHead.hash == commitHash.String()
			}
		}
		if commitCreated {
			session.closed = true
		}

		releaseErr := releaseLocks()
		released = true
		return errors.Join(
			fmt.Errorf("failed to create commit: %w", err),
			outcomeErr,
			wrapRepositoryLockError(releaseErr),
		)
	}

	session.closed = true
	releaseErr := releaseLocks()
	released = true
	if releaseErr != nil {
		return fmt.Errorf("commit created but failed to release repository locks: %w", releaseErr)
	}

	return nil
}

type pathPatternMatcher struct {
	matcher          gitignore.Matcher
	directoryMatcher gitignore.Matcher
}

func validateSelectorPatterns(kind string, patterns []string) error {
	for _, pattern := range patterns {
		if strings.HasPrefix(pattern, "!") {
			return fmt.Errorf(
				"invalid %s pattern %q: include and exclude patterns are positive selectors and do not support negation",
				kind,
				pattern,
			)
		}
	}
	return nil
}

func newPathPatternMatcher(patterns []string) *pathPatternMatcher {
	if len(patterns) == 0 {
		return nil
	}

	parsed := make([]gitignore.Pattern, 0, len(patterns))
	directoryPatterns := make([]gitignore.Pattern, 0, len(patterns))
	for _, pattern := range patterns {
		parsed = append(parsed, gitignore.ParsePattern(pattern, nil))

		directoryPattern := pattern
		if !strings.HasSuffix(directoryPattern, `\ `) {
			directoryPattern = strings.TrimRight(directoryPattern, " ")
		}
		// go-git matches foo/** against foo itself; Git matches only foo's contents.
		if strings.HasSuffix(strings.TrimPrefix(directoryPattern, "!"), "/**") {
			directoryPattern += "/*"
		}
		directoryPatterns = append(
			directoryPatterns,
			gitignore.ParsePattern(directoryPattern, nil),
		)
	}

	return &pathPatternMatcher{
		matcher:          gitignore.NewMatcher(parsed),
		directoryMatcher: gitignore.NewMatcher(directoryPatterns),
	}
}

func (m *pathPatternMatcher) Match(file string) bool {
	if m == nil {
		return false
	}

	path := filepath.ToSlash(file)
	return m.matcher.Match(strings.Split(path, "/"), false)
}

func (m *pathPatternMatcher) MatchGitignore(file string) bool {
	if m == nil {
		return false
	}

	path := strings.Split(filepath.ToSlash(file), "/")
	// Git cannot reinclude a file while one of its parent directories remains ignored.
	for i := 1; i < len(path); i++ {
		if m.directoryMatcher.Match(path[:i], true) {
			return true
		}
	}

	return m.matcher.Match(path, false)
}

func shouldExcludeFile(
	file string,
	excludeMatcher, globalMatcher *pathPatternMatcher,
) bool {
	return globalMatcher.MatchGitignore(file) || excludeMatcher.Match(file)
}

func (g *gitOperations) GetRemoteURL(remoteName string) (string, error) {
	remote, err := g.repo.Remote(remoteName)
	if err != nil {
		return "", fmt.Errorf("failed to get remote '%s': %w", remoteName, err)
	}

	config := remote.Config()
	if len(config.URLs) == 0 {
		return "", fmt.Errorf("remote '%s' has no URLs", remoteName)
	}

	// Return the first URL (usually there's only one)
	return config.URLs[0], nil
}

func (g *gitOperations) GetDefaultBranch() string {
	cmd := exec.Command("git", "symbolic-ref", "refs/remotes/origin/HEAD")
	output, err := cmd.Output()
	if err == nil {
		branch := strings.TrimSpace(string(output))
		if strings.HasPrefix(branch, "refs/remotes/origin/") {
			return strings.TrimPrefix(branch, "refs/remotes/origin/")
		}
	}
	return "master"
}

func (g *gitOperations) Push() (string, error) {
	// Get the current branch name
	branch, err := g.GetCurrentBranch()
	if err != nil {
		return "", fmt.Errorf("failed to get current branch: %w", err)
	}

	// Push to the matching branch on the remote
	cmd := exec.Command("git", "push", "origin", branch)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to push to origin/%s: %w\nOutput: %s", branch, err, string(output))
	}

	// Generate MR/PR URL if possible
	remoteURL, err := g.GetRemoteURL("origin")
	if err != nil {
		// Don't fail the push, just log that we couldn't get the URL
		return "", nil
	}

	remoteInfo, err := parseRemoteURL(remoteURL)
	if err != nil {
		// Don't fail the push, just return empty URL
		return "", nil
	}

	// Get the default/target branch for MR/PR
	targetBranch := g.GetDefaultBranch()

	if branch != targetBranch {
		return generateMergeRequestURL(remoteInfo, branch, targetBranch), nil
	}

	return "", nil
}

// GetLatestTag retrieves the latest semver tag from the repository
func (g *gitOperations) GetLatestTag() (string, error) {
	// Get all tags from git
	cmd := exec.Command("git", "tag", "-l", "v*")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to list tags: %w", err)
	}

	tags := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(tags) == 0 || tags[0] == "" {
		// No tags found, return default
		return "", nil
	}

	// Filter valid semver tags and sort them
	var validTags []string
	semverRegex := regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)
	for _, tag := range tags {
		if semverRegex.MatchString(tag) {
			validTags = append(validTags, tag)
		}
	}

	if len(validTags) == 0 {
		return "", nil
	}

	// Sort tags by semver
	sort.Slice(validTags, func(i, j int) bool {
		vi := parseSemVer(validTags[i])
		vj := parseSemVer(validTags[j])

		if vi.Major != vj.Major {
			return vi.Major > vj.Major
		}
		if vi.Minor != vj.Minor {
			return vi.Minor > vj.Minor
		}
		return vi.Patch > vj.Patch
	})

	return validTags[0], nil
}

// parseSemVer parses a version string like "v1.2.3" into a semVer struct
func parseSemVer(version string) semVer {
	// Remove 'v' prefix if present
	version = strings.TrimPrefix(version, "v")

	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return semVer{0, 0, 0}
	}

	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	patch, _ := strconv.Atoi(parts[2])

	return semVer{
		Major: major,
		Minor: minor,
		Patch: patch,
	}
}

// IncrementVersion increments the version based on the increment type
func (g *gitOperations) IncrementVersion(currentTag string, incrementType string) (string, error) {
	var version semVer

	if currentTag == "" {
		// Start with v0.0.0 if no tags exist
		version = semVer{0, 0, 0}
	} else {
		version = parseSemVer(currentTag)
	}

	switch strings.ToLower(incrementType) {
	case "major":
		version.Major++
		version.Minor = 0
		version.Patch = 0
	case "minor":
		version.Minor++
		version.Patch = 0
	case "patch":
		version.Patch++
	default:
		return "", fmt.Errorf("invalid increment type: %s (must be major, minor, or patch)", incrementType)
	}

	return fmt.Sprintf("v%d.%d.%d", version.Major, version.Minor, version.Patch), nil
}

// CreateTag creates a new annotated tag
func (g *gitOperations) CreateTag(tagName string, message string) error {
	// Create annotated tag
	cmd := exec.Command("git", "tag", "-a", tagName, "-m", message)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to create tag %s: %w\nOutput: %s", tagName, err, string(output))
	}
	return nil
}

// PushTag pushes the tag to the remote repository
func (g *gitOperations) PushTag(tagName string) error {
	cmd := exec.Command("git", "push", "origin", tagName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to push tag %s: %w\nOutput: %s", tagName, err, string(output))
	}
	return nil
}

func shouldIncludeFile(file string, matcher *pathPatternMatcher) bool {
	return matcher.Match(file)
}

func (g *gitOperations) IsGitRepository() bool {
	_, err := g.repo.Head()
	return err == nil || errors.Is(err, plumbing.ErrReferenceNotFound)
}
