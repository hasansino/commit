package openai

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/option"
	"github.com/openai/openai-go/v2/shared"
)

const (
	defaultModel     = shared.ChatModelGPT4Turbo
	defaultMaxTokens = 4096
)

type OpenAI struct {
	apiKey string
	model  string
	client *openai.Client
}

func NewOpenAI() *OpenAI {
	return &OpenAI{
		apiKey: os.Getenv("OPENAI_API_KEY"),
		model:  os.Getenv("OPENAI_MODEL"),
	}
}

func (p *OpenAI) Name() string {
	return "openai"
}

func (p *OpenAI) IsAvailable() bool {
	return p.apiKey != ""
}

func (p *OpenAI) Ask(ctx context.Context, prompt string) ([]string, error) {
	if !p.IsAvailable() {
		return nil, fmt.Errorf("openai api key not found")
	}

	if p.client == nil {
		client := openai.NewClient(
			option.WithAPIKey(p.apiKey),
		)
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

	text := strings.TrimSpace(candidate.Message.Content)
	text = strings.Trim(text, "```") // nolint
	text = strings.Trim(text, "\n")

	return []string{text}, nil
}

func validFinishReason(reason string) bool {
	switch reason {
	case "stop":
		return true
	default:
		return false
	}
}
