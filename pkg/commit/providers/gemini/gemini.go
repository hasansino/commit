package gemini

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"google.golang.org/genai"
)

const (
	defaultModel     = "gemini-2.5-flash-lite"
	defaultMaxTokens = 4096
	defaultTimeout   = 10 * time.Second
)

type Gemini struct {
	apiKey  string
	model   string
	client  *genai.Client
	timeout time.Duration
}

func NewGemini() *Gemini {
	return &Gemini{
		apiKey:  os.Getenv("GEMINI_API_KEY"),
		model:   os.Getenv("GEMINI_MODEL"),
		timeout: defaultTimeout,
	}
}

func (p *Gemini) Name() string {
	return "gemini"
}

func (p *Gemini) IsAvailable() bool {
	return p.apiKey != ""
}

func (p *Gemini) SetTimeout(timeout time.Duration) {
	if timeout > 0 {
		p.timeout = timeout
	}
}

func (p *Gemini) Ask(ctx context.Context, prompt string) ([]string, error) {
	if !p.IsAvailable() {
		return nil, fmt.Errorf("api key not found")
	}

	if p.client == nil {
		httpClient := &http.Client{
			Timeout: p.timeout,
		}
		client, err := genai.NewClient(ctx, &genai.ClientConfig{
			APIKey:     p.apiKey,
			HTTPClient: httpClient,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create genai client: %w", err)
		}
		p.client = client
	}

	contents := []*genai.Content{
		genai.NewContentFromText(prompt, "user"),
	}

	model := defaultModel
	if len(p.model) > 0 {
		model = p.model
	}

	resp, err := p.client.Models.GenerateContent(
		ctx, model, contents,
		&genai.GenerateContentConfig{
			MaxOutputTokens: defaultMaxTokens,
			CandidateCount:  1,
		})
	if err != nil {
		return nil, fmt.Errorf("failed to generate content: %w", err)
	}

	if len(resp.Candidates) == 0 {
		return nil, fmt.Errorf("no candidates received")
	}

	candidate := resp.Candidates[0]

	if len(candidate.FinishReason) > 0 && !validFinishReason(candidate.FinishReason) {
		return nil, fmt.Errorf("stopped with reason: %s", candidate.FinishReason)
	}

	if candidate.Content == nil {
		return nil, fmt.Errorf("no content received")
	}

	var text string
	for _, part := range candidate.Content.Parts {
		if part.Text != "" {
			text += part.Text
		}
	}

	return []string{text}, nil
}

func validFinishReason(reason genai.FinishReason) bool {
	switch reason {
	case genai.FinishReasonStop:
		return true
	default:
		return false
	}
}
