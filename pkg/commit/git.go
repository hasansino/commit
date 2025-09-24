package commit

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type gitOperations struct {
	repo *git.Repository
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
	repo, err := git.PlainOpenWithOptions(repoPath, &git.PlainOpenOptions{
		DetectDotGit: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open git repository: %w", err)
	}

	return &gitOperations{repo: repo}, nil
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
	cmd := exec.Command("git", "config", key)
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

// parseGitignoreFile parses a gitignore file and returns exclude patterns
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
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Skip negation patterns (!) for simplicity in exclude-only logic
		if strings.HasPrefix(line, "!") {
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
	head, err := g.repo.Head()
	if err != nil {
		return "", fmt.Errorf("failed to get HEAD: %w", err)
	}

	branchName := head.Name().Short()
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

	// Build list of files to stage (filtering phase)
	var filesToStage []string
	for file := range status {
		fileStatus := status.File(file)
		if fileStatus.Worktree == git.Unmodified {
			continue
		}

		if shouldExcludeFile(file, excludePatterns, globalPatterns) {
			continue
		}

		if len(includePatterns) > 0 && !shouldIncludeFile(file, includePatterns) {
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

// getFilteredStagedFiles returns list of staged files excluding pre-defined patterns
func (g *gitOperations) getFilteredStagedFiles() ([]string, error) {
	cmd := exec.Command("git", "diff", "--cached", "--name-only")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	files := strings.Split(strings.TrimSpace(string(output)), "\n")

	filtered := make([]string, 0, len(files))
	for _, file := range files {
		if len(file) > 0 { // nothing yet
			filtered = append(filtered, file)
		}
	}

	return filtered, nil
}

func (g *gitOperations) GetStagedDiff(maxSizeBytes int) (string, error) {
	diffFiles, err := g.getFilteredStagedFiles()
	if err != nil {
		return "", fmt.Errorf("failed to get staged files: %w", err)
	}

	if len(diffFiles) == 0 {
		return "", nil // No files to diff after filtering
	}

	// Common diff options optimized for AI consumption
	baseDiffOpts := []string{
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

		cmd := exec.Command("git", contextOpts...)
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

	cmd := exec.Command("git", contextOpts...)
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

func (g *gitOperations) CreateCommit(message string) error {
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

	_, err = worktree.Commit(message, commitOptions)
	if err != nil {
		return fmt.Errorf("failed to create commit: %w", err)
	}

	return nil
}

func shouldExcludeFile(file string, excludePatterns []string, globalPatterns []string) bool {
	// First check global gitignore patterns
	if len(globalPatterns) > 0 {
		basename := filepath.Base(file)
		for _, pattern := range globalPatterns {
			// Handle directory patterns (ending with /)
			if strings.HasSuffix(pattern, "/") {
				dirPattern := strings.TrimSuffix(pattern, "/")
				if strings.Contains(file, dirPattern+"/") {
					return true
				}
			}

			// Fast string containment check first
			if strings.Contains(file, pattern) || strings.Contains(basename, pattern) {
				return true
			}

			// Glob matching for patterns with wildcards
			if matched, _ := filepath.Match(pattern, file); matched {
				return true
			}
			if matched, _ := filepath.Match(pattern, basename); matched {
				return true
			}
		}
	}

	// Then check local exclude patterns (existing logic)
	if len(excludePatterns) == 0 {
		return false
	}

	basename := filepath.Base(file)
	for _, pattern := range excludePatterns {
		// Fast string containment check first (most common case)
		if strings.Contains(file, pattern) || strings.Contains(basename, pattern) {
			return true
		}
		// Expensive glob matching only if simple checks fail
		if matched, _ := filepath.Match(pattern, file); matched {
			return true
		}
		if matched, _ := filepath.Match(pattern, basename); matched {
			return true
		}
	}
	return false
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

func shouldIncludeFile(file string, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}

	basename := filepath.Base(file)
	for _, pattern := range patterns {
		// Fast string containment check first (most common case)
		if strings.Contains(file, pattern) || strings.Contains(basename, pattern) {
			return true
		}
		// Expensive glob matching only if simple checks fail
		if matched, _ := filepath.Match(pattern, file); matched {
			return true
		}
		if matched, _ := filepath.Match(pattern, basename); matched {
			return true
		}
	}
	return false
}

func (g *gitOperations) IsGitRepository() bool {
	_, err := g.repo.Head()
	return err == nil
}
