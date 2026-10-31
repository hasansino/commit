package local

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const ProviderName = "local"

const llamaCLIExecutable = "llama-cli"

const (
	defaultModelFilename = "Qwen3.5-0.8B-Q8_0.gguf"
	defaultModelURL      = "https://huggingface.co/ggml-org/Qwen3.5-0.8B-GGUF/resolve/807dfd9a6fadbf703207aac3bfb0b2b3040d077c/" + defaultModelFilename + "?download=true"
	defaultModelSHA256   = "37ae482d336108d23516fa35e8e0c4126688d81018b87178a18d752a1357814f"
	defaultContextSize   = 32 * 1024
	defaultMaxTokens     = 64
	defaultTimeout       = 10 * time.Second
)

type inferenceFunc func(
	ctx context.Context,
	executable string,
	modelPath string,
	prompt string,
	contextSize uint32,
	maxTokens int,
	timeout time.Duration,
) (string, error)

// Local generates commit messages with a GGUF model through llama-cli.
type Local struct {
	logger         *slog.Logger
	modelSource    string
	expectedSHA256 string
	cacheDir       string
	contextSize    uint32
	maxTokens      int
	timeout        time.Duration
	httpClient     *http.Client
	lookPath       func(string) (string, error)
	generate       inferenceFunc
	configErr      error
}

func NewLocal(logger *slog.Logger) *Local {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	modelSource := strings.TrimSpace(os.Getenv("LOCAL_MODEL"))
	expectedSHA256 := strings.TrimSpace(os.Getenv("LOCAL_MODEL_SHA256"))
	if modelSource == "" {
		modelSource = defaultModelURL
		if expectedSHA256 == "" {
			expectedSHA256 = defaultModelSHA256
		}
	}

	cacheDir := strings.TrimSpace(os.Getenv("LOCAL_MODEL_CACHE_DIR"))
	if cacheDir == "" {
		cacheDir = defaultCacheDir()
	}

	contextSize, contextErr := positiveIntEnv("LOCAL_CONTEXT_SIZE", defaultContextSize)
	if contextSize < 0 || contextSize > math.MaxUint32 {
		contextErr = fmt.Errorf("LOCAL_CONTEXT_SIZE must fit in an unsigned 32-bit integer")
		contextSize = 0
	}
	maxTokens, maxTokensErr := positiveIntEnv("LOCAL_MAX_TOKENS", defaultMaxTokens)

	provider := &Local{
		logger:         logger,
		modelSource:    modelSource,
		expectedSHA256: strings.ToLower(expectedSHA256),
		cacheDir:       cacheDir,
		contextSize:    uint32(contextSize),
		maxTokens:      maxTokens,
		timeout:        defaultTimeout,
		httpClient:     http.DefaultClient,
		lookPath:       exec.LookPath,
		generate:       generateWithLlamaCLI,
	}
	if contextErr != nil {
		provider.configErr = contextErr
	} else if maxTokensErr != nil {
		provider.configErr = maxTokensErr
	} else if maxTokens >= contextSize {
		provider.configErr = fmt.Errorf(
			"LOCAL_MAX_TOKENS (%d) must be smaller than LOCAL_CONTEXT_SIZE (%d)",
			maxTokens,
			contextSize,
		)
	}
	return provider
}

func (p *Local) Name() string {
	return ProviderName
}

// IsAvailable is true because the provider is registered only when explicitly
// selected. Ask returns a specific installation error when llama-cli is absent.
func (p *Local) IsAvailable() bool {
	return true
}

// SetTimeout sets the inference timeout. Model preparation and download use
// the parent context and are not included in this duration.
func (p *Local) SetTimeout(timeout time.Duration) {
	if timeout > 0 {
		p.timeout = timeout
	}
}

func (p *Local) IsLocal() bool {
	return true
}

func (p *Local) Ask(ctx context.Context, prompt string) ([]string, error) {
	if p.configErr != nil {
		return nil, p.configErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	executable := llamaCLIExecutable
	if p.lookPath != nil {
		var err error
		executable, err = p.lookPath(llamaCLIExecutable)
		if err != nil {
			return nil, fmt.Errorf(
				"%s not found in PATH; install llama.cpp first (macOS/Homebrew: brew install llama.cpp)",
				llamaCLIExecutable,
			)
		}
	}

	modelPath, err := p.resolveModel(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare local model: %w", err)
	}

	text, err := p.generate(
		ctx,
		executable,
		modelPath,
		prompt,
		p.contextSize,
		p.maxTokens,
		p.timeout,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to generate with local model: %w", err)
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("local model returned no content")
	}
	return []string{text}, nil
}

func defaultCacheDir() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = os.TempDir()
	}
	return filepath.Join(cacheDir, "commit", "models")
}

func positiveIntEnv(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer, got %q", name, value)
	}
	return parsed, nil
}
