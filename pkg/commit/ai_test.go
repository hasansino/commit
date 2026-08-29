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
		ctx, diff, branch, files, providers, "", false,
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

func TestAIService_GenerateCommitMessages_AllProviders(t *testing.T) {
	ctrl := gomock.NewController(t)

	firstProvider := mocks.NewMockproviderAccessor(ctrl)
	firstProvider.EXPECT().Name().Return("provider1").AnyTimes()
	firstProvider.EXPECT().Ask(gomock.Any(), gomock.Any()).Return([]string{"first message"}, nil)

	secondProvider := mocks.NewMockproviderAccessor(ctrl)
	secondProvider.EXPECT().Name().Return("provider2").AnyTimes()
	secondProvider.EXPECT().Ask(gomock.Any(), gomock.Any()).Return([]string{"second message"}, nil)

	service := &aiService{
		logger:  slog.New(slog.DiscardHandler),
		timeout: 30 * time.Second,
		providers: map[string]providerAccessor{
			"provider1": firstProvider,
			"provider2": secondProvider,
		},
	}

	messages, err := service.GenerateCommitMessages(
		context.Background(), "diff", "main", []string{"file.go"}, nil, "", false,
	)
	if err != nil {
		t.Fatalf("GenerateCommitMessages() error = %v", err)
	}

	if len(messages) != 2 {
		t.Fatalf("GenerateCommitMessages() returned %d messages, want 2", len(messages))
	}
	if messages["provider1"] != "first message" {
		t.Errorf("provider1 message = %q, want %q", messages["provider1"], "first message")
	}
	if messages["provider2"] != "second message" {
		t.Errorf("provider2 message = %q, want %q", messages["provider2"], "second message")
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
		ctx, diff, branch, files, providers, "", false,
	)

	if err == nil {
		t.Error("GenerateCommitMessages() expected error for no providers but got none")
	}

	expectedError := "no ai providers available"
	if err.Error() != expectedError {
		t.Errorf("GenerateCommitMessages() error = %q, want %q", err.Error(), expectedError)
	}
}

func TestAIService_GenerateCommitMessages_RejectsUnusableMessages(t *testing.T) {
	tests := []struct {
		name     string
		messages []string
	}{
		{name: "no messages", messages: nil},
		{name: "blank after cleanup", messages: []string{" \n\t "}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			provider := mocks.NewMockproviderAccessor(ctrl)
			provider.EXPECT().Name().Return("unusable").AnyTimes()
			provider.EXPECT().Ask(gomock.Any(), gomock.Any()).Return(tt.messages, nil)

			service := &aiService{
				logger:  slog.New(slog.DiscardHandler),
				timeout: time.Second,
				providers: map[string]providerAccessor{
					"unusable": provider,
				},
			}

			messages, err := service.GenerateCommitMessages(
				context.Background(), "diff", "main", []string{"file.go"}, nil, "", false,
			)
			if err != nil {
				t.Fatalf("GenerateCommitMessages() error = %v", err)
			}
			if len(messages) != 0 {
				t.Fatalf("GenerateCommitMessages() = %#v, want no messages", messages)
			}
		})
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
		ctx, diff, branch, files, providers, "", false,
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
		ctx, diff, branch, files, providers, "", false,
	)

	if err != nil {
		t.Errorf("GenerateCommitMessages() unexpected error = %v", err)
	}

	// Should return empty messages since provider failed
	if len(messages) != 0 {
		t.Errorf("GenerateCommitMessages() with failing provider should return empty messages, got %d", len(messages))
	}
}

func TestAIService_cleanupMessage(t *testing.T) {
	service := &aiService{}

	tests := []struct {
		name string
		in   string
		out  string
	}{
		{
			name: "leading and trailing whitespace",
			in:   "   test   ",
			out:  "test",
		},
		{
			name: "empty string",
			in:   "",
			out:  "",
		},
		{
			name: "only whitespace",
			in:   " \t \n  ",
			out:  "",
		},
		{
			name: "newline trimming",
			in:   "\n\ntest\n\n",
			out:  "test",
		},
		{
			name: "single fenced block inline",
			in:   "```hello```",
			out:  "hello",
		},
		{
			name: "fenced with newlines",
			in:   "```\nhello world\n```",
			out:  "hello world",
		},
		{
			name: "fenced with language hint",
			in:   "```gitcommit\nfeat: add login\n```",
			out:  "feat: add login",
		},
		{
			name: "fenced with generic language hint",
			in:   "```text\nfeat: add login\n\nbody\n```",
			out:  "feat: add login\n\nbody",
		},
		{
			name: "fenced with spaced language hint and CRLF",
			in:   "``` gitcommit\r\nfeat: add login\r\n```",
			out:  "feat: add login",
		},
		{
			name: "language name on content line is preserved",
			in:   "```\ngitcommit\nfeat: add login\n```",
			out:  "gitcommit\nfeat: add login",
		},
		{
			name: "inline language name is content",
			in:   "```gitcommit```",
			out:  "gitcommit",
		},
		{
			name: "drop outside of fences",
			in:   "prefix\n```\ninside\n```\nsuffix",
			out:  "inside",
		},
		//{
		//	name: "only opening fence",
		//	in:   "```\ninside",
		//	out:  "inside",
		//},
		//{
		//	name: "only closing fence",
		//	in:   "inside\n```",
		//	out:  "inside",
		//},
		{
			name: "inline single backticks untouched",
			in:   "`inline` code",
			out:  "`inline` code",
		},
		{
			name: "multiple fenced sections uses outermost",
			in:   "```first```\ntext\n```second```",
			out:  "first```\ntext\n```second",
		},
		{
			name: "empty fenced content",
			in:   "``````",
			out:  "",
		},
		{
			name: "empty tagged fenced content",
			in:   "```text\n```",
			out:  "",
		},
		{
			name: "overlapping fences are left untouched",
			in:   "````",
			out:  "````",
		},
		{
			name: "spaces inside fenced content trimmed",
			in:   "```  hello  ```",
			out:  "hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.cleanupMessage(tt.in)
			if result != tt.out {
				t.Fatalf("cleanupMessage() = %q, want %q", result, tt.out)
			}
		})
	}
}
