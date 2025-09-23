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
	"github.com/hasansino/commit/pkg/commit/providers/openai"

	_ "embed"
)

//go:embed prompt.md
var defaultPrompt string

//go:embed prompt-format-single.md
var promptFormatSingle string

//go:embed prompt-format-multi.md
var promptFormatMulti string

type aiService struct {
	logger    *slog.Logger
	timeout   time.Duration
	providers map[string]providerAccessor
}

func newAIService(logger *slog.Logger, timeout time.Duration) *aiService {
	providerList := make(map[string]providerAccessor)

	if openaiProvider := openai.NewOpenAI(); openaiProvider.IsAvailable() {
		providerList[openaiProvider.Name()] = openaiProvider
	}
	if claudeProvider := claude.NewClaude(); claudeProvider.IsAvailable() {
		providerList[claudeProvider.Name()] = claudeProvider
	}
	if geminiProvider := gemini.NewGemini(); geminiProvider.IsAvailable() {
		providerList[geminiProvider.Name()] = geminiProvider
	}

	return &aiService{
		logger:    logger,
		timeout:   timeout,
		providers: providerList,
	}
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
		if provider, exists := s.providers[strings.ToLower(name)]; exists {
			filtered[provider.Name()] = s.providers[provider.Name()]
		}
	}
	return filtered
}

func (s *aiService) GenerateCommitMessages(
	ctx context.Context,
	diff, branch string, files []string,
	providers []string, customPrompt string,
	first bool, multiLine bool,
) (map[string]string, error) {
	// passed from --providers(-p) flag
	activeProviders := s.FilterProviders(providers)
	if len(activeProviders) == 0 {
		return nil, fmt.Errorf("no ai providers available")
	}

	var prompt string
	if len(customPrompt) > 0 {
		prompt = s.buildCustomPrompt(customPrompt, diff, branch, files)
	} else {
		prompt = s.buildPrompt(diff, branch, files, multiLine)
	}

	type providerResponse struct {
		Name    string
		Message string
	}

	commonCtx, commonCtxCancel := context.WithCancel(ctx)

	wg := &sync.WaitGroup{}
	resultChan := make(chan providerResponse, len(activeProviders))

	for _, provider := range activeProviders {
		wg.Add(1)
		go func(ctx context.Context, provider providerAccessor) {
			defer wg.Done()

			s.logger.DebugContext(
				ctx, "Requesting message from provider",
				"provider", provider.Name(),
			)

			ctx, cancel := context.WithTimeout(ctx, s.timeout)
			defer cancel()

			messages, err := provider.Ask(ctx, prompt)
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					s.logger.ErrorContext(
						ctx, "Failed to request message from provider",
						"provider", provider.Name(),
						"error", err.Error(),
					)
				}
				return
			}

			if len(messages) == 0 {
				s.logger.WarnContext(
					ctx, "No messages received from provider",
					"provider", provider.Name(),
				)
				return
			}

			resultChan <- providerResponse{
				Name:    provider.Name(),
				Message: messages[0],
			}
		}(commonCtx, provider)
	}

	results := make(map[string]string)

	// we want first fastest response
	if first {
		msg := <-resultChan
		results[msg.Name] = msg.Message
		commonCtxCancel()
		wg.Wait()
		close(resultChan)
		return results, nil
	}

	wg.Wait()
	commonCtxCancel()
	close(resultChan)
	for result := range resultChan {
		results[result.Name] = result.Message
	}
	return results, nil
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
