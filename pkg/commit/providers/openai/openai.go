package openai

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

const ProviderName = "openai"

const (
	defaultModel     = shared.ChatModelGPT4oMini
	defaultMaxTokens = 4096
	defaultTimeout   = 10 * time.Second
)

type OpenAI struct {
	apiKey          string
	model           string
	client          *openai.Client
	timeout         time.Duration
	unsafeAllowHTTP bool
}

func NewOpenAI() *OpenAI {
	return &OpenAI{
		apiKey:          os.Getenv("OPENAI_API_KEY"),
		model:           os.Getenv("OPENAI_MODEL"),
		timeout:         defaultTimeout,
		unsafeAllowHTTP: os.Getenv("OPENAI_UNSAFE_ALLOW_HTTP") == "1",
	}
}

func (p *OpenAI) Name() string { return ProviderName }

func (p *OpenAI) IsAvailable() bool {
	return p.apiKey != ""
}

func (p *OpenAI) SetTimeout(timeout time.Duration) {
	if timeout > 0 {
		p.timeout = timeout
	}
}

func (p *OpenAI) IsLocal() bool { return false }

func (p *OpenAI) Ask(ctx context.Context, prompt string) ([]string, error) {
	if !p.IsAvailable() {
		return nil, fmt.Errorf("openai api key not found")
	}

	if p.client == nil {
		httpClient := &http.Client{
			Timeout: p.timeout,
		}
		options := []option.RequestOption{
			option.WithAPIKey(p.apiKey),
			option.WithHTTPClient(httpClient),
		}
		if p.unsafeAllowHTTP {
			// The SDK confines this opt-in to direct loopback connections for local fixtures.
			options = append(options, option.WithUnsafeAllowHTTP())
		}
		client := openai.NewClient(options...)
		p.client = &client
	}

	model := defaultModel
	if len(p.model) > 0 {
		model = p.model
	}

	chatCompletion, err := p.client.Chat.Completions.New(
		ctx, openai.ChatCompletionNewParams{
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage(prompt),
			},
			Model:               model,
			MaxCompletionTokens: openai.Int(defaultMaxTokens),
			N:                   openai.Int(1), // number of candidates
		})
	if err != nil {
		return nil, fmt.Errorf("failed to create completion: %w", err)
	}

	if len(chatCompletion.Choices) == 0 {
		return nil, fmt.Errorf("no candidates received")
	}

	candidate := chatCompletion.Choices[0]

	// "stop", "length", "tool_calls", "content_filter", "function_call"
	if len(candidate.FinishReason) > 0 && !validFinishReason(candidate.FinishReason) {
		return nil, fmt.Errorf("stopped with reason: %s", candidate.FinishReason)
	}

	if len(candidate.Message.Content) == 0 {
		return nil, fmt.Errorf("no content received")
	}

	return []string{candidate.Message.Content}, nil
}

func validFinishReason(reason string) bool {
	switch reason {
	case "stop":
		return true
	default:
		return false
	}
}
