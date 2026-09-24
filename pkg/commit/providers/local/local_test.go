package local

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewLocalDefaults(t *testing.T) {
	t.Setenv("LOCAL_MODEL", "")
	t.Setenv("LOCAL_MODEL_SHA256", "")
	t.Setenv("LOCAL_MODEL_CACHE_DIR", "")
	t.Setenv("LOCAL_CONTEXT_SIZE", "")
	t.Setenv("LOCAL_MAX_TOKENS", "")

	provider := NewLocal(nil)

	if provider.Name() != ProviderName {
		t.Fatalf("Name() = %q, want %q", provider.Name(), ProviderName)
	}
	if !provider.IsAvailable() {
		t.Fatal("IsAvailable() = false, want true")
	}
	if provider.modelSource != defaultModelURL {
		t.Errorf("model source = %q, want default URL", provider.modelSource)
	}
	if provider.expectedSHA256 != defaultModelSHA256 {
		t.Errorf("model checksum = %q, want pinned default checksum", provider.expectedSHA256)
	}
	if provider.contextSize != defaultContextSize {
		t.Errorf("context size = %d, want %d", provider.contextSize, defaultContextSize)
	}
	if provider.maxTokens != defaultMaxTokens {
		t.Errorf("max tokens = %d, want %d", provider.maxTokens, defaultMaxTokens)
	}
	if provider.timeout != defaultTimeout {
		t.Errorf("timeout = %s, want %s", provider.timeout, defaultTimeout)
	}
}

func TestLocalAskUsesConfiguredModelFile(t *testing.T) {
	modelPath := filepath.Join(t.TempDir(), "custom.gguf")
	if err := os.WriteFile(modelPath, []byte("GGUFtest-model"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOCAL_MODEL", modelPath)
	t.Setenv("LOCAL_MODEL_SHA256", "")

	provider := NewLocal(slog.New(slog.DiscardHandler))
	provider.lookPath = func(name string) (string, error) {
		if name != llamaCLIExecutable {
			t.Errorf("executable = %q, want %q", name, llamaCLIExecutable)
		}
		return "/test/bin/llama-cli", nil
	}
	provider.generate = func(
		_ context.Context,
		executable string,
		gotPath string,
		gotPrompt string,
		contextSize uint32,
		maxTokens int,
		timeout time.Duration,
	) (string, error) {
		if executable != "/test/bin/llama-cli" {
			t.Errorf("executable = %q", executable)
		}
		if gotPath != modelPath {
			t.Errorf("model path = %q, want %q", gotPath, modelPath)
		}
		if gotPrompt != "make a commit message" {
			t.Errorf("prompt = %q", gotPrompt)
		}
		if contextSize != defaultContextSize || maxTokens != defaultMaxTokens {
			t.Errorf("generation settings = (%d, %d)", contextSize, maxTokens)
		}
		if timeout != defaultTimeout {
			t.Errorf("inference timeout = %s, want %s", timeout, defaultTimeout)
		}
		return "feat(local): generate offline", nil
	}

	messages, err := provider.Ask(context.Background(), "make a commit message")
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if len(messages) != 1 || messages[0] != "feat(local): generate offline" {
		t.Fatalf("Ask() = %#v", messages)
	}
}

func TestLocalDownloadsVerifiesAndCachesModel(t *testing.T) {
	t.Setenv("HF_TOKEN", "must-not-leak-to-custom-host")
	modelData := []byte("GGUFdownloaded-model")
	checksum := sha256.Sum256(modelData)
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestCount.Add(1)
		if request.Header.Get("Accept") != "application/octet-stream" {
			t.Errorf("Accept header = %q", request.Header.Get("Accept"))
		}
		if authorization := request.Header.Get("Authorization"); authorization != "" {
			t.Errorf("Authorization header leaked to custom host: %q", authorization)
		}
		// This exceeds the inference timeout below. The download must still
		// finish because that timeout starts only after model preparation.
		time.Sleep(100 * time.Millisecond)
		writer.Header().Set("Content-Length", "20")
		_, _ = writer.Write(modelData)
	}))
	defer server.Close()

	cacheDir := t.TempDir()
	provider := &Local{
		logger:         slog.New(slog.DiscardHandler),
		modelSource:    server.URL + "/model.gguf",
		expectedSHA256: hex.EncodeToString(checksum[:]),
		cacheDir:       cacheDir,
		contextSize:    defaultContextSize,
		maxTokens:      defaultMaxTokens,
		timeout:        50 * time.Millisecond,
		httpClient:     server.Client(),
		generate: func(
			ctx context.Context,
			_ string,
			path string,
			_ string,
			_ uint32,
			_ int,
			timeout time.Duration,
		) (string, error) {
			if path != filepath.Join(cacheDir, "model.gguf") {
				t.Errorf("model path = %q", path)
			}
			if err := ctx.Err(); err != nil {
				t.Errorf("parent context expired during download: %v", err)
			}
			if timeout != 50*time.Millisecond {
				t.Errorf("inference timeout = %s", timeout)
			}
			return "fix(local): cache model", nil
		},
	}

	for range 2 {
		messages, err := provider.Ask(context.Background(), "prompt")
		if err != nil {
			t.Fatalf("Ask() error = %v", err)
		}
		if len(messages) != 1 || messages[0] != "fix(local): cache model" {
			t.Fatalf("Ask() = %#v", messages)
		}
	}
	if requestCount.Load() != 1 {
		t.Errorf("download request count = %d, want 1", requestCount.Load())
	}
	downloaded, err := os.ReadFile(filepath.Join(cacheDir, "model.gguf"))
	if err != nil {
		t.Fatal(err)
	}
	if string(downloaded) != string(modelData) {
		t.Errorf("downloaded model = %q", downloaded)
	}
	modelPath := filepath.Join(cacheDir, "model.gguf")
	modelInfo, err := os.Stat(modelPath)
	if err != nil {
		t.Fatal(err)
	}
	if !modelVerificationMatches(modelPath, provider.expectedSHA256, modelInfo) {
		t.Fatal("downloaded model verification was not cached")
	}
	matches, err := filepath.Glob(filepath.Join(cacheDir, "*.part"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("temporary downloads remain: %v", matches)
	}
}

func TestLocalCachedModelRevalidatesAfterFileChange(t *testing.T) {
	modelData := []byte("GGUFdownloaded-model")
	checksum := sha256.Sum256(modelData)
	modelPath := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(modelPath, modelData, 0o600); err != nil {
		t.Fatal(err)
	}

	provider := &Local{
		logger:         slog.New(slog.DiscardHandler),
		expectedSHA256: hex.EncodeToString(checksum[:]),
	}
	if err := provider.validateCachedModelFile(modelPath); err != nil {
		t.Fatalf("first validation error = %v", err)
	}

	tampered := append([]byte(nil), modelData...)
	tampered[len(tampered)-1] ^= 1
	if err := os.WriteFile(modelPath, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(modelPath, future, future); err != nil {
		t.Fatal(err)
	}
	if err := provider.validateCachedModelFile(modelPath); err == nil ||
		!strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("validation error = %v, want checksum mismatch", err)
	}
}

func TestLocalRejectsChecksumMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "GGUFwrong-model")
	}))
	defer server.Close()

	cacheDir := t.TempDir()
	provider := &Local{
		logger:         slog.New(slog.DiscardHandler),
		modelSource:    server.URL + "/model.gguf",
		expectedSHA256: strings.Repeat("0", 64),
		cacheDir:       cacheDir,
		contextSize:    defaultContextSize,
		maxTokens:      defaultMaxTokens,
		httpClient:     server.Client(),
		generate: func(context.Context, string, string, string, uint32, int, time.Duration) (string, error) {
			t.Fatal("generation must not run for an invalid download")
			return "", nil
		},
	}

	_, err := provider.Ask(context.Background(), "prompt")
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Ask() error = %v, want checksum mismatch", err)
	}
	if _, statErr := os.Stat(filepath.Join(cacheDir, "model.gguf")); !os.IsNotExist(statErr) {
		t.Fatalf("invalid model was installed: %v", statErr)
	}
}

func TestLocalRejectsInvalidGenerationSettings(t *testing.T) {
	t.Setenv("LOCAL_CONTEXT_SIZE", "128")
	t.Setenv("LOCAL_MAX_TOKENS", "128")

	provider := NewLocal(slog.New(slog.DiscardHandler))
	_, err := provider.Ask(context.Background(), "prompt")
	if err == nil || !strings.Contains(err.Error(), "must be smaller") {
		t.Fatalf("Ask() error = %v, want invalid settings error", err)
	}
}

func TestLocalContextSizeBounds(t *testing.T) {
	t.Setenv("LOCAL_MAX_TOKENS", "64")
	t.Run("maximum representable size", func(t *testing.T) {
		t.Setenv("LOCAL_CONTEXT_SIZE", "4294967295")
		provider := NewLocal(nil)
		if provider.configErr != nil || provider.contextSize != 4294967295 {
			t.Fatalf("context size = %d, error = %v", provider.contextSize, provider.configErr)
		}
	})
	t.Run("overflow rejected before inference", func(t *testing.T) {
		t.Setenv("LOCAL_CONTEXT_SIZE", "4294967296")
		provider := NewLocal(nil)
		_, err := provider.Ask(context.Background(), "prompt")
		if err == nil || !strings.Contains(err.Error(), "LOCAL_CONTEXT_SIZE") {
			t.Fatalf("Ask() error = %v, want context size validation error", err)
		}
	})
}

func TestLocalRequiresLlamaCLI(t *testing.T) {
	provider := NewLocal(slog.New(slog.DiscardHandler))
	provider.lookPath = func(string) (string, error) {
		return "", errors.New("not found")
	}
	provider.generate = func(context.Context, string, string, string, uint32, int, time.Duration) (string, error) {
		t.Fatal("generation must not run without llama-cli")
		return "", nil
	}

	_, err := provider.Ask(context.Background(), "prompt")
	if err == nil || !strings.Contains(err.Error(), "brew install llama.cpp") {
		t.Fatalf("Ask() error = %v, want llama-cli installation instructions", err)
	}
}
