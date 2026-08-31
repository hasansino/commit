package commit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/hasansino/commit/pkg/commit/providers/claude"
	"github.com/hasansino/commit/pkg/commit/providers/gemini"
	"github.com/hasansino/commit/pkg/commit/providers/local"
	"github.com/hasansino/commit/pkg/commit/providers/openai"

	_ "embed"
)

//go:embed prompt.md
var defaultPrompt string

//go:embed prompt-format-single.md
var promptFormatSingle string

//go:embed prompt-format-multi.md
var promptFormatMulti string

const localMaxDiffSizeBytes = 24 * 1024

type aiService struct {
	logger    *slog.Logger
	timeout   time.Duration
	providers map[string]providerAccessor
}

func newAIService(logger *slog.Logger, timeout time.Duration, requestedProviders []string) (*aiService, error) {
	providerList := make(map[string]providerAccessor)

	for _, requestedProvider := range requestedProviders {
		var provider providerAccessor
		switch strings.ToLower(strings.TrimSpace(requestedProvider)) {
		case openai.ProviderName:
			provider = openai.NewOpenAI()
		case claude.ProviderName:
			provider = claude.NewClaude()
		case gemini.ProviderName:
			provider = gemini.NewGemini()
		case local.ProviderName:
			provider = local.NewLocal(logger)
		}
		if provider == nil || !provider.IsAvailable() {
			continue
		}
		provider.SetTimeout(timeout)
		providerList[provider.Name()] = provider
	}

	if len(providerList) == 0 {
		return nil, errors.New("no AI providers available")
	}

	return &aiService{
		logger:    logger,
		timeout:   timeout,
		providers: providerList,
	}, nil
}

func (s *aiService) NumProviders() int {
	return len(s.providers)
}

func (s *aiService) FilterProviders(requested []string) map[string]providerAccessor {
	if len(requested) == 0 {
		return s.providers
	}
	filtered := make(map[string]providerAccessor)
	for _, name := range requested {
		if provider, exists := s.providers[name]; exists {
			filtered[provider.Name()] = s.providers[provider.Name()]
		}
	}
	return filtered
}

func (s *aiService) GenerateCommitMessages(
	ctx context.Context,
	diff, branch string, files []string,
	providers []string, customPrompt string,
	multiLine bool,
) (map[string]string, error) {
	// passed from --providers(-p) flag
	activeProviders := s.FilterProviders(providers)
	if len(activeProviders) == 0 {
		return nil, fmt.Errorf("no ai providers available")
	}

	type providerResponse struct {
		Name    string
		Message string
		Time    time.Duration
		Err     error
	}

	var wg sync.WaitGroup
	resultChan := make(chan providerResponse, len(activeProviders))

	for _, provider := range activeProviders {
		isLocal := provider.IsLocal()
		providerDiff := diff
		if isLocal {
			providerDiff = compactUnifiedDiff(diff, localMaxDiffSizeBytes)
		}

		var prompt string
		if len(customPrompt) > 0 {
			prompt = s.buildCustomPrompt(customPrompt, providerDiff, branch, files)
		} else {
			prompt = s.buildPrompt(providerDiff, branch, files, multiLine)
		}

		wg.Add(1)
		go func(ctx context.Context, provider providerAccessor, prompt string, isLocal bool) {
			defer wg.Done()

			s.logger.DebugContext(
				ctx, "Requesting message from provider",
				"provider", provider.Name(),
			)

			started := time.Now()
			messages, err := s.askProvider(ctx, provider, prompt, isLocal)
			duration := time.Since(started)

			if err != nil {
				if !errors.Is(err, context.Canceled) {
					s.logger.ErrorContext(
						ctx, "Failed to request message from provider",
						"provider", provider.Name(),
						"error", err.Error(),
					)
				}
				resultChan <- providerResponse{
					Name: provider.Name(),
					Err:  err,
					Time: duration,
				}
				return
			}

			if len(messages) == 0 {
				s.logger.WarnContext(
					ctx, "No messages received from provider",
					"provider", provider.Name(),
				)
				resultChan <- providerResponse{
					Name: provider.Name(),
					Err:  errors.New("no messages received from provider"),
					Time: duration,
				}
				return
			}

			message := s.cleanupMessage(messages[0])
			if message == "" {
				err := errors.New("empty message received from provider")
				s.logger.WarnContext(
					ctx, "Empty message received from provider",
					"provider", provider.Name(),
				)
				resultChan <- providerResponse{
					Name: provider.Name(),
					Err:  err,
					Time: duration,
				}
				return
			}

			resultChan <- providerResponse{
				Name:    provider.Name(),
				Message: message,
				Time:    duration,
			}
		}(ctx, provider, prompt, isLocal)
	}

	results := make(map[string]string)

	wg.Wait()
	close(resultChan)
	for result := range resultChan {
		if result.Err != nil {
			s.logger.ErrorContext(
				ctx, "Failed to get message from provider",
				"provider", result.Name,
				"error", result.Err.Error(),
			)
		} else {
			results[result.Name] = result.Message
		}
		s.logger.DebugContext(
			ctx, "Received response from provider",
			"provider", result.Name,
			"time", result.Time.String(),
		)
	}

	return results, nil
}

func (s *aiService) askProvider(
	ctx context.Context,
	provider providerAccessor,
	prompt string,
	isLocal bool,
) ([]string, error) {
	if isLocal {
		return provider.Ask(ctx, prompt)
	}
	requestCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	return provider.Ask(requestCtx, prompt)
}

func (s *aiService) cleanupMessage(message string) string {
	const fence = "```"

	start := strings.Index(message, fence)
	end := strings.LastIndex(message, fence)

	contentStart := start + len(fence)
	if start != -1 && end >= contentStart {
		message = message[contentStart:end]

		// A fenced block may have an info string, such as "gitcommit", on
		// the opening fence's line. Drop that line without changing inline
		// fenced content or the existing outermost-fence behavior.
		lineEnd := strings.IndexByte(message, '\n')
		nextFence := strings.Index(message, fence)
		if lineEnd != -1 && (nextFence == -1 || lineEnd < nextFence) {
			message = message[lineEnd+1:]
		}
	}

	return strings.TrimSpace(message)
}

func (s *aiService) buildPrompt(diff, branch string, files []string, multiLine bool) string {
	injectFormat := promptFormatSingle
	if multiLine {
		injectFormat = promptFormatMulti
	}
	result := defaultPrompt
	result = strings.ReplaceAll(result, "{format}", injectFormat)
	result = strings.ReplaceAll(result, "{branch}", branch)
	result = strings.ReplaceAll(result, "{files}", strings.Join(files, ", "))
	result = strings.ReplaceAll(result, "{diff}", diff)
	return result
}

func (s *aiService) buildCustomPrompt(prompt string, diff, branch string, files []string) string {
	result := strings.ReplaceAll(prompt, "{branch}", branch)
	result = strings.ReplaceAll(result, "{files}", strings.Join(files, ", "))
	result = strings.ReplaceAll(result, "{diff}", diff)
	return result
}
