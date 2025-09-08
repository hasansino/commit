package commit

import (
	"testing"
)

func TestParseRemoteURL(t *testing.T) {
	tests := []struct {
		name      string
		remoteURL string
		wantInfo  *RemoteInfo
		wantErr   bool
	}{
		{
			name:      "GitHub HTTPS URL",
			remoteURL: "https://github.com/owner/repo.git",
			wantInfo: &RemoteInfo{
				Platform: PlatformGitHub,
				Host:     "github.com",
				Owner:    "owner",
				Repo:     "repo",
			},
			wantErr: false,
		},
		{
			name:      "GitHub HTTPS URL without .git",
			remoteURL: "https://github.com/owner/repo",
			wantInfo: &RemoteInfo{
				Platform: PlatformGitHub,
				Host:     "github.com",
				Owner:    "owner",
				Repo:     "repo",
			},
			wantErr: false,
		},
		{
			name:      "GitHub SSH URL",
			remoteURL: "git@github.com:owner/repo.git",
			wantInfo: &RemoteInfo{
				Platform: PlatformGitHub,
				Host:     "github.com",
				Owner:    "owner",
				Repo:     "repo",
			},
			wantErr: false,
		},
		{
			name:      "GitHub SSH URL with ssh://",
			remoteURL: "ssh://git@github.com/owner/repo.git",
			wantInfo: &RemoteInfo{
				Platform: PlatformGitHub,
				Host:     "github.com",
				Owner:    "owner",
				Repo:     "repo",
			},
			wantErr: false,
		},
		{
			name:      "GitLab HTTPS URL",
			remoteURL: "https://gitlab.com/owner/repo.git",
			wantInfo: &RemoteInfo{
				Platform: PlatformGitLab,
				Host:     "gitlab.com",
				Owner:    "owner",
				Repo:     "repo",
			},
			wantErr: false,
		},
		{
			name:      "GitLab SSH URL",
			remoteURL: "git@gitlab.com:owner/repo.git",
			wantInfo: &RemoteInfo{
				Platform: PlatformGitLab,
				Host:     "gitlab.com",
				Owner:    "owner",
				Repo:     "repo",
			},
			wantErr: false,
		},
		{
			name:      "GitLab with subgroup HTTPS",
			remoteURL: "https://gitlab.com/group/subgroup/repo.git",
			wantInfo: &RemoteInfo{
				Platform: PlatformGitLab,
				Host:     "gitlab.com",
				Owner:    "group/subgroup",
				Repo:     "repo",
			},
			wantErr: false,
		},
		{
			name:      "GitLab with subgroup SSH",
			remoteURL: "git@gitlab.com:group/subgroup/repo.git",
			wantInfo: &RemoteInfo{
				Platform: PlatformGitLab,
				Host:     "gitlab.com",
				Owner:    "group/subgroup",
				Repo:     "repo",
			},
			wantErr: false,
		},
		{
			name:      "Self-hosted GitLab",
			remoteURL: "https://gitlab.example.com/owner/repo.git",
			wantInfo: &RemoteInfo{
				Platform: PlatformGitLab,
				Host:     "gitlab.example.com",
				Owner:    "owner",
				Repo:     "repo",
			},
			wantErr: false,
		},
		{
			name:      "Self-hosted GitHub Enterprise",
			remoteURL: "https://github.enterprise.com/owner/repo.git",
			wantInfo: &RemoteInfo{
				Platform: PlatformGitHub,
				Host:     "github.enterprise.com",
				Owner:    "owner",
				Repo:     "repo",
			},
			wantErr: false,
		},
		{
			name:      "Unknown platform",
			remoteURL: "https://bitbucket.org/owner/repo.git",
			wantInfo: &RemoteInfo{
				Platform: PlatformUnknown,
				Host:     "bitbucket.org",
				Owner:    "owner",
				Repo:     "repo",
			},
			wantErr: false,
		},
		{
			name:      "Empty URL",
			remoteURL: "",
			wantInfo:  nil,
			wantErr:   true,
		},
		{
			name:      "Invalid URL format",
			remoteURL: "not-a-url",
			wantInfo:  nil,
			wantErr:   true,
		},
		{
			name:      "HTTP URL with port",
			remoteURL: "https://github.com:8080/owner/repo.git",
			wantInfo: &RemoteInfo{
				Platform: PlatformGitHub,
				Host:     "github.com:8080",
				Owner:    "owner",
				Repo:     "repo",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := parseRemoteURL(tt.remoteURL)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseRemoteURL() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && info != nil {
				if info.Platform != tt.wantInfo.Platform {
					t.Errorf("Platform = %v, want %v", info.Platform, tt.wantInfo.Platform)
				}
				if info.Host != tt.wantInfo.Host {
					t.Errorf("Host = %v, want %v", info.Host, tt.wantInfo.Host)
				}
				if info.Owner != tt.wantInfo.Owner {
					t.Errorf("Owner = %v, want %v", info.Owner, tt.wantInfo.Owner)
				}
				if info.Repo != tt.wantInfo.Repo {
					t.Errorf("Repo = %v, want %v", info.Repo, tt.wantInfo.Repo)
				}
			}
		})
	}
}

func TestGenerateMergeRequestURL(t *testing.T) {
	tests := []struct {
		name         string
		info         *RemoteInfo
		branch       string
		targetBranch string
		wantURL      string
	}{
		{
			name: "GitHub PR URL with target branch",
			info: &RemoteInfo{
				Platform: PlatformGitHub,
				Host:     "github.com",
				Owner:    "owner",
				Repo:     "repo",
			},
			branch:       "feature-branch",
			targetBranch: "master",
			wantURL:      "https://github.com/owner/repo/compare/master...feature-branch?expand=1",
		},
		{
			name: "GitHub PR URL without target branch",
			info: &RemoteInfo{
				Platform: PlatformGitHub,
				Host:     "github.com",
				Owner:    "owner",
				Repo:     "repo",
			},
			branch:       "feature-branch",
			targetBranch: "",
			wantURL:      "https://github.com/owner/repo/pull/new/feature-branch",
		},
		{
			name: "GitLab MR URL with target branch",
			info: &RemoteInfo{
				Platform: PlatformGitLab,
				Host:     "gitlab.com",
				Owner:    "owner",
				Repo:     "repo",
			},
			branch:       "feature-branch",
			targetBranch: "master",
			wantURL:      "https://gitlab.com/owner/repo/-/merge_requests/new?merge_request%5Bsource_branch%5D=feature-branch&merge_request%5Btarget_branch%5D=master",
		},
		{
			name: "GitLab MR URL without target branch",
			info: &RemoteInfo{
				Platform: PlatformGitLab,
				Host:     "gitlab.com",
				Owner:    "owner",
				Repo:     "repo",
			},
			branch:       "feature-branch",
			targetBranch: "",
			wantURL:      "https://gitlab.com/owner/repo/-/merge_requests/new?merge_request%5Bsource_branch%5D=feature-branch",
		},
		{
			name: "GitLab MR URL with subgroup",
			info: &RemoteInfo{
				Platform: PlatformGitLab,
				Host:     "gitlab.com",
				Owner:    "group/subgroup",
				Repo:     "repo",
			},
			branch:       "feature-branch",
			targetBranch: "master",
			wantURL:      "https://gitlab.com/group/subgroup/repo/-/merge_requests/new?merge_request%5Bsource_branch%5D=feature-branch&merge_request%5Btarget_branch%5D=master",
		},
		{
			name: "GitHub with special characters in branch",
			info: &RemoteInfo{
				Platform: PlatformGitHub,
				Host:     "github.com",
				Owner:    "owner",
				Repo:     "repo",
			},
			branch:       "feature/new-feature",
			targetBranch: "develop",
			wantURL:      "https://github.com/owner/repo/compare/develop...feature%2Fnew-feature?expand=1",
		},
		{
			name: "Unknown platform returns empty",
			info: &RemoteInfo{
				Platform: PlatformUnknown,
				Host:     "bitbucket.org",
				Owner:    "owner",
				Repo:     "repo",
			},
			branch:       "feature-branch",
			targetBranch: "master",
			wantURL:      "",
		},
		{
			name:         "Nil info returns empty",
			info:         nil,
			branch:       "feature-branch",
			targetBranch: "master",
			wantURL:      "",
		},
		{
			name: "Empty branch returns empty",
			info: &RemoteInfo{
				Platform: PlatformGitHub,
				Host:     "github.com",
				Owner:    "owner",
				Repo:     "repo",
			},
			branch:       "",
			targetBranch: "master",
			wantURL:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotURL := generateMergeRequestURL(tt.info, tt.branch, tt.targetBranch)
			if gotURL != tt.wantURL {
				t.Errorf("generateMergeRequestURL() = %v, want %v", gotURL, tt.wantURL)
			}
		})
	}
}

func TestDetectPlatform(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		wantPlat GitPlatform
	}{
		{"GitHub.com", "github.com", PlatformGitHub},
		{"GitHub Enterprise", "github.enterprise.com", PlatformGitHub},
		{"GitLab.com", "gitlab.com", PlatformGitLab},
		{"Self-hosted GitLab", "gitlab.example.com", PlatformGitLab},
		{"Mixed case GitHub", "GitHub.com", PlatformGitHub},
		{"Mixed case GitLab", "GitLab.com", PlatformGitLab},
		{"Bitbucket", "bitbucket.org", PlatformUnknown},
		{"Generic Git", "git.example.com", PlatformUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := detectPlatform(tt.host); got != tt.wantPlat {
				t.Errorf("detectPlatform() = %v, want %v", got, tt.wantPlat)
			}
		})
	}
}
