package commit

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

type GitPlatform string

const (
	PlatformGitHub  GitPlatform = "github"
	PlatformGitLab  GitPlatform = "gitlab"
	PlatformUnknown GitPlatform = "unknown"
)

type RemoteInfo struct {
	Platform GitPlatform
	Host     string
	Owner    string
	Repo     string
}

// parseRemoteURL parses a git remote URL and extracts platform information
func parseRemoteURL(remoteURL string) (*RemoteInfo, error) {
	if remoteURL == "" {
		return nil, fmt.Errorf("empty remote URL")
	}

	info := &RemoteInfo{
		Platform: PlatformUnknown,
	}

	// Check if it's an HTTP(S) URL first
	if strings.HasPrefix(remoteURL, "http://") || strings.HasPrefix(remoteURL, "https://") {
		// Handle HTTPS URLs
		u, err := url.Parse(remoteURL)
		if err != nil {
			return nil, fmt.Errorf("failed to parse URL: %w", err)
		}

		info.Host = u.Host
		
		// Extract owner and repo from path
		pathParts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(pathParts) >= 2 {
			info.Owner = pathParts[0]
			info.Repo = strings.TrimSuffix(pathParts[1], ".git")
			
			// Handle GitLab subgroups (multiple path segments)
			if strings.Contains(info.Host, "gitlab") && len(pathParts) > 2 {
				// For GitLab, owner can be a nested group
				info.Owner = strings.Join(pathParts[:len(pathParts)-1], "/")
				info.Repo = strings.TrimSuffix(pathParts[len(pathParts)-1], ".git")
			}
		} else {
			return nil, fmt.Errorf("invalid repository path in URL")
		}
	} else {
		// Handle SSH URLs (git@host:owner/repo.git or git@host:group/subgroup/repo.git)
		sshPattern := regexp.MustCompile(`^(?:ssh://)?(?:git@)?([^:/]+)[:/](.+?)(?:\.git)?$`)
		if matches := sshPattern.FindStringSubmatch(remoteURL); len(matches) == 3 {
			info.Host = matches[1]
			
			// Split the path to handle both simple and nested paths
			pathParts := strings.Split(matches[2], "/")
			if len(pathParts) >= 2 {
				// For GitLab, handle subgroups
				if strings.Contains(strings.ToLower(matches[1]), "gitlab") && len(pathParts) > 2 {
					info.Owner = strings.Join(pathParts[:len(pathParts)-1], "/")
					info.Repo = strings.TrimSuffix(pathParts[len(pathParts)-1], ".git")
				} else {
					info.Owner = pathParts[0]
					info.Repo = strings.TrimSuffix(strings.Join(pathParts[1:], "/"), ".git")
				}
			} else {
				return nil, fmt.Errorf("invalid repository path in SSH URL")
			}
		} else {
			return nil, fmt.Errorf("unsupported URL format: %s", remoteURL)
		}
	}

	// Detect platform based on host
	info.Platform = detectPlatform(info.Host)

	return info, nil
}

// detectPlatform identifies the git platform from the host
func detectPlatform(host string) GitPlatform {
	lowerHost := strings.ToLower(host)
	
	if strings.Contains(lowerHost, "github") {
		return PlatformGitHub
	}
	if strings.Contains(lowerHost, "gitlab") {
		return PlatformGitLab
	}
	
	return PlatformUnknown
}

// generateMergeRequestURL generates the appropriate MR/PR URL based on platform
func generateMergeRequestURL(info *RemoteInfo, branch string, targetBranch string) string {
	if info == nil || branch == "" {
		return ""
	}

	// URL-encode branch names to handle special characters
	encodedBranch := url.QueryEscape(branch)
	encodedTargetBranch := url.QueryEscape(targetBranch)

	switch info.Platform {
	case PlatformGitHub:
		// GitHub PR URL format
		// https://github.com/{owner}/{repo}/compare/{target}...{branch}?expand=1
		if targetBranch != "" && targetBranch != branch {
			return fmt.Sprintf("https://%s/%s/%s/compare/%s...%s?expand=1",
				info.Host, info.Owner, info.Repo, encodedTargetBranch, encodedBranch)
		}
		// If no target branch or same as source, use simpler format
		return fmt.Sprintf("https://%s/%s/%s/pull/new/%s",
			info.Host, info.Owner, info.Repo, encodedBranch)

	case PlatformGitLab:
		// GitLab MR URL format
		// https://gitlab.com/{owner}/{repo}/-/merge_requests/new?merge_request[source_branch]={branch}&merge_request[target_branch]={target}
		baseURL := fmt.Sprintf("https://%s/%s/%s/-/merge_requests/new",
			info.Host, info.Owner, info.Repo)
		
		params := url.Values{}
		params.Set("merge_request[source_branch]", branch)
		if targetBranch != "" && targetBranch != branch {
			params.Set("merge_request[target_branch]", targetBranch)
		}
		
		return fmt.Sprintf("%s?%s", baseURL, params.Encode())

	default:
		// Unknown platform, return empty string
		return ""
	}
}