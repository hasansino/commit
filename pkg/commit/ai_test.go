package commit

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/hasansino/commit/pkg/commit/mocks"
)

func TestNewAIService(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	timeout := 30 * time.Second

	service := newAIService(logger, timeout)

	if service == nil {
		t.Fatal("NewAIService() returned nil")
	}

	if service.logger != logger {
		t.Errorf("NewAIService() logger = %v, want %v", service.logger, logger)
	}

	if service.timeout != timeout {
		t.Errorf("NewAIService() timeout = %v, want %v", service.timeout, timeout)
	}

	if service.providers == nil {
		t.Error("NewAIService() providers should not be nil")
	}

	// Verify providers map is initialized (even if empty due to missing env vars)
	if service.providers == nil {
		t.Error("NewAIService() should initialize providers map")
	}

	// Verify NumProviders works correctly
	numProviders := service.NumProviders()
	if numProviders < 0 {
		t.Error("NumProviders() should return non-negative value")
	}

	// Verify all providers in the map are valid
	for name, provider := range service.providers {
		if provider == nil {
			t.Errorf("Provider %s should not be nil", name)
		}
		if !provider.IsAvailable() {
			t.Logf("Provider %s is not available (likely missing env vars)", name)
		}
	}
}

func TestAIService_NumProviders(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	service := newAIService(logger, 30*time.Second)

	numProviders := service.NumProviders()

	if numProviders < 0 {
		t.Error("NumProviders() should return non-negative value")
	}

	// Verify the internal providers map is valid
	for name, provider := range service.providers {
		if name == "" {
			t.Error("Provider name should not be empty")
		}
		if provider == nil {
			t.Errorf("Provider %s should not be nil", name)
		}
		if provider.Name() != name {
			t.Errorf("Provider key %s does not match provider name %s", name, provider.Name())
		}
	}

	// Verify NumProviders matches actual count
	if numProviders != len(service.providers) {
		t.Errorf("NumProviders() = %d, want %d", numProviders, len(service.providers))
	}
}

func TestAIService_FilterProviders(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockProvider1 := mocks.NewMockproviderAccessor(ctrl)
	mockProvider1.EXPECT().Name().Return("openai").AnyTimes()

	mockProvider2 := mocks.NewMockproviderAccessor(ctrl)
	mockProvider2.EXPECT().Name().Return("claude").AnyTimes()

	service := &aiService{
		logger:  slog.New(slog.DiscardHandler),
		timeout: 30 * time.Second,
		providers: map[string]providerAccessor{
			"openai": mockProvider1,
			"claude": mockProvider2,
		},
	}

	tests := []struct {
		name      string
		requested []string
		want      []string
	}{
		{
			name:      "empty request returns all",
			requested: []string{},
			want:      []string{"openai", "claude"},
		},
		{
			name:      "specific provider",
			requested: []string{"openai"},
			want:      []string{"openai"},
		},
		{
			name:      "case insensitive",
			requested: []string{"OpenAI"},
			want:      []string{"openai"},
		},
		{
			name:      "multiple providers",
			requested: []string{"openai", "claude"},
			want:      []string{"openai", "claude"},
		},
		{
			name:      "non-existent provider",
			requested: []string{"nonexistent"},
			want:      []string{},
		},
		{
			name:      "mixed existing and non-existing",
			requested: []string{"openai", "nonexistent"},
			want:      []string{"openai"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.FilterProviders(tt.requested)

			if len(result) != len(tt.want) {
				t.Errorf("FilterProviders() returned %d providers, want %d", len(result), len(tt.want))
				return
			}

			for _, wantProvider := range tt.want {
				if _, exists := result[wantProvider]; !exists {
					t.Errorf("FilterProviders() missing expected provider %s", wantProvider)
				}
			}
		})
	}
}

func TestAIService_buildPrompt(t *testing.T) {
	service := &aiService{}

	diff := "diff --git a/test.go b/test.go"
	branch := "feature/test"
	files := []string{"test.go", "main.go"}

	tests := []struct {
		name      string
		multiLine bool
	}{
		{
			name:      "single line format",
			multiLine: false,
		},
		{
			name:      "multi line format",
			multiLine: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.buildPrompt(diff, branch, files, tt.multiLine)

			if result == "" {
				t.Error("buildPrompt() returned empty string")
			}

			// Check that placeholders were replaced
			if strings.Contains(result, "{diff}") {
				t.Error("buildPrompt() did not replace {diff} placeholder")
			}
			if strings.Contains(result, "{branch}") {
				t.Error("buildPrompt() did not replace {branch} placeholder")
			}
			if strings.Contains(result, "{files}") {
				t.Error("buildPrompt() did not replace {files} placeholder")
			}
			if strings.Contains(result, "{format}") {
				t.Error("buildPrompt() did not replace {format} placeholder")
			}

			// Check content was injected
			if !strings.Contains(result, diff) {
				t.Error("buildPrompt() did not include diff content")
			}
			if !strings.Contains(result, branch) {
				t.Error("buildPrompt() did not include branch content")
			}
			if !strings.Contains(result, "test.go, main.go") {
				t.Error("buildPrompt() did not include files content")
			}
		})
	}
}

func TestAIService_buildCustomPrompt(t *testing.T) {
	service := &aiService{}

	tests := []struct {
		name             string
		customPrompt     string
		diff             string
		branch           string
		files            []string
		expectations     []string
		shouldNotContain []string
	}{
		{
			name:             "basic placeholder replacement",
			customPrompt:     "Generate a commit message for branch {branch} with files {files} and diff {diff}",
			diff:             "diff --git a/test.go b/test.go\n+func test() {}",
			branch:           "feature/test",
			files:            []string{"test.go", "main.go"},
			expectations:     []string{"feature/test", "test.go, main.go", "diff --git a/test.go b/test.go"},
			shouldNotContain: []string{"{branch}", "{files}", "{diff}"},
		},
		{
			name:             "no placeholders",
			customPrompt:     "Simple prompt with no variables",
			diff:             "some diff",
			branch:           "feature",
			files:            []string{"file.go"},
			expectations:     []string{"Simple prompt with no variables"},
			shouldNotContain: []string{},
		},
		{
			name:             "multiple file formatting",
			customPrompt:     "Changed files: {files}",
			diff:             "diff",
			branch:           "branch",
			files:            []string{"file1.go", "file2.js", "file3.py"},
			expectations:     []string{"file1.go, file2.js, file3.py"},
			shouldNotContain: []string{"{files}"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.buildCustomPrompt(tt.customPrompt, tt.diff, tt.branch, tt.files)

			if result == "" && tt.customPrompt != "" {
				t.Error("buildCustomPrompt() returned empty string for non-empty prompt")
			}

			// Check expected content
			for _, expected := range tt.expectations {
				if !strings.Contains(result, expected) {
					t.Errorf("buildCustomPrompt() result should contain %q, got: %s", expected, result)
				}
			}

			// Check content that should not be present
			for _, shouldNotContain := range tt.shouldNotContain {
				if strings.Contains(result, shouldNotContain) {
					t.Errorf("buildCustomPrompt() result should not contain %q, got: %s", shouldNotContain, result)
				}
			}
		})
	}
}

func TestAIService_GenerateCommitMessages(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockProvider := mocks.NewMockproviderAccessor(ctrl)
	mockProvider.EXPECT().Name().Return("testprovider").AnyTimes()
	mockProvider.EXPECT().Ask(gomock.Any(), gomock.Any()).Return([]string{"test commit message"}, nil)

	service := &aiService{
		logger:  slog.New(slog.DiscardHandler),
		timeout: 30 * time.Second,
		providers: map[string]providerAccessor{
			"testprovider": mockProvider,
		},
	}

	ctx := context.Background()
	diff := "diff --git a/test.go b/test.go"
	branch := "master"
	files := []string{"test.go"}
	providers := []string{"testprovider"}

	messages, err := service.GenerateCommitMessages(
		ctx, diff, branch, files, providers, "", false, false,
	)

	if err != nil {
		t.Errorf("GenerateCommitMessages() unexpected error = %v", err)
	}

	if len(messages) != 1 {
		t.Errorf("GenerateCommitMessages() returned %d messages, want 1", len(messages))
	}

	if messages["testprovider"] != "test commit message" {
		t.Errorf("GenerateCommitMessages() = %q, want %q", messages["testprovider"], "test commit message")
	}
}

func TestAIService_GenerateCommitMessages_NoProviders(t *testing.T) {
	service := &aiService{
		logger:    slog.New(slog.DiscardHandler),
		timeout:   30 * time.Second,
		providers: map[string]providerAccessor{},
	}

	ctx := context.Background()
	diff := "diff --git a/test.go b/test.go"
	branch := "master"
	files := []string{"test.go"}
	providers := []string{"nonexistent"}

	_, err := service.GenerateCommitMessages(
		ctx, diff, branch, files, providers, "", false, false,
	)

	if err == nil {
		t.Error("GenerateCommitMessages() expected error for no providers but got none")
	}

	expectedError := "no ai providers available"
	if err.Error() != expectedError {
		t.Errorf("GenerateCommitMessages() error = %q, want %q", err.Error(), expectedError)
	}
}

func TestAIService_GenerateCommitMessages_FirstMode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockProvider1 := mocks.NewMockproviderAccessor(ctrl)
	mockProvider1.EXPECT().Name().Return("provider1").AnyTimes()
	mockProvider1.EXPECT().Ask(gomock.Any(), gomock.Any()).Return([]string{"first message"}, nil).AnyTimes()

	mockProvider2 := mocks.NewMockproviderAccessor(ctrl)
	mockProvider2.EXPECT().Name().Return("provider2").AnyTimes()
	mockProvider2.EXPECT().Ask(gomock.Any(), gomock.Any()).Return([]string{"second message"}, nil).AnyTimes()

	service := &aiService{
		logger:  slog.New(slog.DiscardHandler),
		timeout: 30 * time.Second,
		providers: map[string]providerAccessor{
			"provider1": mockProvider1,
			"provider2": mockProvider2,
		},
	}

	ctx := context.Background()
	diff := "diff --git a/test.go b/test.go\n+func test() {}"
	branch := "master"
	files := []string{"test.go"}
	providers := []string{}

	messages, err := service.GenerateCommitMessages(
		ctx, diff, branch, files, providers, "", true, false, // first = true
	)

	if err != nil {
		t.Errorf("GenerateCommitMessages() unexpected error = %v", err)
	}

	if len(messages) != 1 {
		t.Errorf("GenerateCommitMessages() with first=true returned %d messages, want 1", len(messages))
	}

	// Verify we got exactly one message from one of the providers
	foundValidMessage := false
	for providerName, message := range messages {
		if providerName == "provider1" || providerName == "provider2" {
			if message != "" {
				foundValidMessage = true
			}
		}
	}
	if !foundValidMessage {
		t.Error("Expected to find a valid message from one of the providers")
	}
}

func TestAIService_GenerateCommitMessages_ContextCancellation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockProvider := mocks.NewMockproviderAccessor(ctrl)
	mockProvider.EXPECT().Name().Return("testprovider").AnyTimes()
	mockProvider.EXPECT().Ask(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, prompt string) ([]string, error) {
			// Simulate slow provider that gets cancelled
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(100 * time.Millisecond):
				return []string{"slow message"}, nil
			}
		},
	)

	service := &aiService{
		logger:  slog.New(slog.DiscardHandler),
		timeout: 30 * time.Second,
		providers: map[string]providerAccessor{
			"testprovider": mockProvider,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	diff := "diff --git a/test.go b/test.go\n+func test() {}"
	branch := "master"
	files := []string{"test.go"}
	providers := []string{"testprovider"}

	messages, err := service.GenerateCommitMessages(
		ctx, diff, branch, files, providers, "", false, false,
	)

	if err != nil {
		t.Errorf("GenerateCommitMessages() unexpected error = %v", err)
	}

	// Should return empty messages since context was cancelled
	if len(messages) != 0 {
		t.Errorf("GenerateCommitMessages() with cancelled context should return empty messages, got %d", len(messages))
	}
}

func TestAIService_GenerateCommitMessages_ProviderError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockProvider := mocks.NewMockproviderAccessor(ctrl)
	mockProvider.EXPECT().Name().Return("errorprovider").AnyTimes()
	mockProvider.EXPECT().Ask(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("provider error"))

	service := &aiService{
		logger:  slog.New(slog.DiscardHandler),
		timeout: 30 * time.Second,
		providers: map[string]providerAccessor{
			"errorprovider": mockProvider,
		},
	}

	ctx := context.Background()
	diff := "diff --git a/test.go b/test.go\n+func test() {}"
	branch := "master"
	files := []string{"test.go"}
	providers := []string{"errorprovider"}

	messages, err := service.GenerateCommitMessages(
		ctx, diff, branch, files, providers, "", false, false,
	)

	if err != nil {
		t.Errorf("GenerateCommitMessages() unexpected error = %v", err)
	}

	// Should return empty messages since provider failed
	if len(messages) != 0 {
		t.Errorf("GenerateCommitMessages() with failing provider should return empty messages, got %d", len(messages))
	}
}
