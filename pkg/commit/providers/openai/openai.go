package openai

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

const ProviderName = "openai"

const (
	defaultModel     = shared.ChatModelGPT6Luna
	defaultMaxTokens = 4096
	defaultTimeout   = 10 * time.Second
)

type OpenAI struct {
	apiKey          string
	model           string
	baseURL         string
	client          *openai.Client
	timeout         time.Duration
	unsafeAllowHTTP bool
}

func NewOpenAI() *OpenAI {
	return &OpenAI{
		apiKey:          os.Getenv("OPENAI_API_KEY"),
		model:           os.Getenv("OPENAI_MODEL"),
		baseURL:         os.Getenv("OPENAI_BASE_URL"),
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
		if err := validateBaseURL(p.baseURL, p.unsafeAllowHTTP); err != nil {
			return nil, err
		}
		httpClient := &http.Client{
			Timeout: p.timeout,
		}
		options := []option.RequestOption{
			option.WithAPIKey(p.apiKey),
			option.WithHTTPClient(httpClient),
		}
		if p.baseURL != "" {
			// Send requests to the validated endpoint, not to the SDK's own environment lookup.
			options = append(options, option.WithBaseURL(p.baseURL))
		}
		client := openai.NewClient(options...)
		p.client = &client
	}

	model := defaultModel
	if len(p.model) > 0 {
		model = p.model
	}

	params := openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage(prompt),
		},
		Model:               model,
		MaxCompletionTokens: openai.Int(defaultMaxTokens),
		N:                   openai.Int(1), // number of candidates
	}
	if model == defaultModel {
		// Short commit messages do not need a separate reasoning budget.
		params.ReasoningEffort = shared.ReasoningEffortNone
	}
	chatCompletion, err := p.client.Chat.Completions.New(ctx, params)
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

// validateBaseURL rejects endpoints that would send the API key in plaintext.
// Plain HTTP requires an explicit opt-in and is limited to loopback hosts.
// @note openai-go made option.WithUnsafeAllowHTTP a no-op after v3.69.0, so the
// SDK no longer enforces this.
func validateBaseURL(baseURL string, allowHTTP bool) error {
	if baseURL == "" {
		// The SDK default endpoint uses HTTPS.
		return nil
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		// Omit the parse error, which repeats the URL and any credentials in it.
		return errors.New("invalid OPENAI_BASE_URL")
	}
	switch {
	case u.Scheme == "https":
		return nil
	case u.Scheme == "http" && allowHTTP && isLoopbackHost(u.Hostname()):
		return nil
	default:
		return errors.New(
			"authenticated requests require HTTPS; " +
				"OPENAI_UNSAFE_ALLOW_HTTP=1 permits HTTP only for loopback endpoints",
		)
	}
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validFinishReason(reason string) bool {
	switch reason {
	case "stop":
		return true
	default:
		return false
	}
}
