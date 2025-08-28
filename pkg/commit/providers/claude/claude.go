package claude

import (
	"context"
	"fmt"
	"os"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const (
	defaultModel     = "claude-3-5-haiku-latest"
	defaultMaxTokens = 4096
)

type Claude struct {
	apiKey string
	model  string
	client *anthropic.Client
}

func NewClaude() *Claude {
	return &Claude{
		apiKey: os.Getenv("ANTHROPIC_API_KEY"),
		model:  os.Getenv("ANTHROPIC_MODEL"),
	}
}

func (p *Claude) Name() string {
	return "claude"
}

func (p *Claude) IsAvailable() bool {
	return p.apiKey != ""
}

func (p *Claude) Ask(ctx context.Context, prompt string) ([]string, error) {
	if !p.IsAvailable() {
		return nil, fmt.Errorf("api key not found")
	}

	if p.client == nil {
		client := anthropic.NewClient(
			option.WithAPIKey(p.apiKey),
		)
		p.client = &client
	}

	model := defaultModel
	if len(p.model) > 0 {
		model = p.model
	}

	message, err := p.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: int64(defaultMaxTokens),
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create message: %w", err)
	}

	// "end_turn", "max_tokens", "stop_sequence", "tool_use", "pause_turn", "refusal"
	if len(message.StopReason) != 0 && !validStopReason(message.StopReason) {
		return nil, fmt.Errorf("stopped with reason: %s", message.StopReason)
	}

	if len(message.Content) == 0 {
		return nil, fmt.Errorf("no text content received")
	}

	var text string
	for _, content := range message.Content {
		if content.Type == "text" {
			textBlock := content.AsText()
			text += textBlock.Text
		}
	}

	return []string{text}, nil
}

func validStopReason(reason anthropic.StopReason) bool {
	switch reason {
	case anthropic.StopReasonEndTurn:
		return true
	default:
		return false
	}
}
