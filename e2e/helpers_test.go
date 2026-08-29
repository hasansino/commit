package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/onsi/gomega/gexec"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	providerOpenAI    = "openai"
	providerClaude    = "claude"
	providerGemini    = "gemini"
	defaultAIResponse = "feat(core): exercise the public workflow"
	commandTimeout    = 15 * time.Second
)

type commandResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

func (r commandResult) Output() string {
	return r.Stdout + r.Stderr
}

type runOptions struct {
	API          *fakeAI
	Providers    []string
	Environment  map[string]string
	GlobalConfig string
	Stdin        string
	Timeout      time.Duration
}

func openAIOptions(api *fakeAI) runOptions {
	return runOptions{API: api, Providers: []string{providerOpenAI}}
}

func runCLI(
	ctx context.Context,
	workingDirectory string,
	options runOptions,
	arguments ...string,
) commandResult {
	GinkgoHelper()

	session := startCLI(ctx, workingDirectory, options, arguments...)
	return resultFromSession(ctx, session, options.Timeout)
}

func resultFromSession(ctx context.Context, session *gexec.Session, timeout time.Duration) commandResult {
	GinkgoHelper()

	if timeout == 0 {
		timeout = commandTimeout
	}
	Eventually(session).WithContext(ctx).WithTimeout(timeout).Should(gexec.Exit())

	return commandResult{
		ExitCode: session.ExitCode(),
		Stdout:   string(session.Out.Contents()),
		Stderr:   string(session.Err.Contents()),
	}
}

func startCLI(
	ctx context.Context,
	workingDirectory string,
	options runOptions,
	arguments ...string,
) *gexec.Session {
	GinkgoHelper()

	command := exec.CommandContext(ctx, commitBinary, arguments...)
	command.Dir = workingDirectory
	command.Env = childEnvironment(options)
	if options.Stdin != "" {
		command.Stdin = strings.NewReader(options.Stdin)
	}

	session, err := gexec.Start(command, GinkgoWriter, GinkgoWriter)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { stopSession(session) })
	return session
}

func stopSession(session *gexec.Session) {
	if session.ExitCode() == -1 {
		session.Kill()
	}

	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-session.Exited:
	case <-timer.C:
		Fail("timed out waiting for CLI process cleanup")
	}
}

func childEnvironment(options runOptions) []string {
	overrides := map[string]string{
		"GIT_CONFIG_GLOBAL":   options.GlobalConfig,
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_TERMINAL_PROMPT": "0",
		"LANG":                "C",
		"LC_ALL":              "C",
		"NO_COLOR":            "1",
		"TERM":                "xterm-256color",
	}
	if overrides["GIT_CONFIG_GLOBAL"] == "" {
		overrides["GIT_CONFIG_GLOBAL"] = os.DevNull
	}

	if options.API != nil {
		for _, provider := range options.Providers {
			switch strings.ToLower(provider) {
			case providerOpenAI:
				overrides["OPENAI_API_KEY"] = "e2e-openai-key"
				overrides["OPENAI_MODEL"] = "e2e-openai-model"
				overrides["OPENAI_BASE_URL"] = options.API.URL() + "/v1/"
			case providerClaude:
				overrides["ANTHROPIC_API_KEY"] = "e2e-claude-key"
				overrides["ANTHROPIC_MODEL"] = "e2e-claude-model"
				overrides["ANTHROPIC_BASE_URL"] = options.API.URL() + "/"
			case providerGemini:
				overrides["GEMINI_API_KEY"] = "e2e-gemini-key"
				overrides["GEMINI_MODEL"] = "e2e-gemini-model"
				overrides["GOOGLE_GEMINI_BASE_URL"] = options.API.URL() + "/"
			}
		}
	}
	for key, value := range options.Environment {
		overrides[key] = value
	}

	// Force all non-fixture HTTP traffic through a local rejecting proxy. Apply
	// these last so future test options cannot accidentally enable real network access.
	overrides["ALL_PROXY"] = outboundDenyProxyURL
	overrides["HTTP_PROXY"] = outboundDenyProxyURL
	overrides["HTTPS_PROXY"] = outboundDenyProxyURL
	overrides["NO_PROXY"] = ""
	if options.API != nil {
		overrides["NO_PROXY"] = strings.TrimPrefix(options.API.URL(), "http://")
	}

	blockedPrefixes := []string{"COMMIT_", "OPENAI_", "ANTHROPIC_", "GEMINI_", "GOOGLE_", "GIT_"}
	blockedNames := map[string]bool{
		"ALL_PROXY": true, "HTTP_PROXY": true, "HTTPS_PROXY": true,
		"LANG": true, "LC_ALL": true, "NO_COLOR": true, "NO_PROXY": true, "TERM": true,
	}
	environment := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		upperKey := strings.ToUpper(key)
		blocked := blockedNames[upperKey]
		for _, prefix := range blockedPrefixes {
			if strings.HasPrefix(upperKey, prefix) {
				blocked = true
				break
			}
		}
		if !blocked {
			environment[key] = value
		}
	}
	for key, value := range overrides {
		environment[key] = value
	}

	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+environment[key])
	}
	return result
}

type gitRepository struct {
	Path         string
	GlobalConfig string
	InitialHead  string
}

func newRepository() *gitRepository {
	GinkgoHelper()
	return newRepositoryWithInitialCommit(true)
}

func newUnbornRepository() *gitRepository {
	GinkgoHelper()
	return newRepositoryWithInitialCommit(false)
}

func newRepositoryWithInitialCommit(initialCommit bool) *gitRepository {
	GinkgoHelper()

	root := GinkgoT().TempDir()
	repositoryPath := filepath.Join(root, "repo")
	Expect(os.MkdirAll(repositoryPath, 0o755)).To(Succeed())
	globalConfig := filepath.Join(root, "global.gitconfig")
	Expect(os.WriteFile(globalConfig, nil, 0o600)).To(Succeed())

	repository := &gitRepository{Path: repositoryPath, GlobalConfig: globalConfig}
	repository.git("init", "--initial-branch=main")
	repository.git("config", "user.name", "E2E User")
	repository.git("config", "user.email", "e2e@example.test")
	repository.git("config", "commit.gpgsign", "false")
	repository.git("config", "tag.gpgSign", "false")
	repository.git("config", "core.hooksPath", filepath.Join(root, "disabled-hooks"))

	if initialCommit {
		repository.write("tracked.txt", "initial\n")
		repository.git("add", "--", "tracked.txt")
		repository.git("commit", "--no-gpg-sign", "-m", "chore: initial state")
		repository.InitialHead = strings.TrimSpace(repository.git("rev-parse", "HEAD"))
	}
	return repository
}

func (r *gitRepository) write(name, contents string) {
	GinkgoHelper()
	path := filepath.Join(r.Path, filepath.FromSlash(name))
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(contents), 0o644)).To(Succeed())
}

func (r *gitRepository) append(name, contents string) {
	GinkgoHelper()
	path := filepath.Join(r.Path, filepath.FromSlash(name))
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	Expect(err).NotTo(HaveOccurred())
	_, err = file.WriteString(contents)
	Expect(err).NotTo(HaveOccurred())
	Expect(file.Close()).To(Succeed())
}

func (r *gitRepository) read(name string) string {
	GinkgoHelper()
	contents, err := os.ReadFile(filepath.Join(r.Path, filepath.FromSlash(name)))
	Expect(err).NotTo(HaveOccurred())
	return string(contents)
}

func (r *gitRepository) git(arguments ...string) string {
	GinkgoHelper()
	output, err := r.tryGit(arguments...)
	Expect(err).NotTo(HaveOccurred(), "git %s failed:\n%s", strings.Join(arguments, " "), output)
	return output
}

func (r *gitRepository) tryGit(arguments ...string) (string, error) {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "git", append([]string{"-C", r.Path}, arguments...)...)
	command.Env = childEnvironment(runOptions{GlobalConfig: r.GlobalConfig})
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return string(output), fmt.Errorf("git %s timed out: %w", strings.Join(arguments, " "), ctx.Err())
	}
	return string(output), err
}

func (r *gitRepository) head() string {
	GinkgoHelper()
	return strings.TrimSpace(r.git("rev-parse", "HEAD"))
}

func (r *gitRepository) commitCount() string {
	GinkgoHelper()
	return strings.TrimSpace(r.git("rev-list", "--count", "HEAD"))
}

func (r *gitRepository) commitMessage() string {
	GinkgoHelper()
	return strings.TrimSpace(r.git("log", "-1", "--format=%B"))
}

func (r *gitRepository) status() string {
	GinkgoHelper()
	return r.git("status", "--porcelain=v1", "--untracked-files=all")
}

func (r *gitRepository) cachedDiff() string {
	GinkgoHelper()
	return r.git("diff", "--cached", "--no-color", "--")
}

func (r *gitRepository) indexBytes() []byte {
	GinkgoHelper()
	contents, err := os.ReadFile(r.gitPath("index"))
	Expect(err).NotTo(HaveOccurred())
	return contents
}

func (r *gitRepository) gitPath(name string) string {
	GinkgoHelper()
	path := strings.TrimSpace(r.git("rev-parse", "--git-path", name))
	if !filepath.IsAbs(path) {
		path = filepath.Join(r.Path, path)
	}
	return filepath.Clean(path)
}

func newBareRemote(globalConfig string) string {
	GinkgoHelper()
	path := filepath.Join(GinkgoT().TempDir(), "remote.git")
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "init", "--bare", "--initial-branch=main", path)
	command.Env = childEnvironment(runOptions{GlobalConfig: globalConfig})
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		Fail(fmt.Sprintf("initialize bare Git remote timed out: %v\n%s", ctx.Err(), output))
	}
	Expect(err).NotTo(HaveOccurred(), "initialize bare Git remote:\n%s", output)
	return path
}

type apiReply struct {
	Message string
	Status  int
	Body    string
	Delay   time.Duration
	Release <-chan struct{}
}

type apiRequest struct {
	Provider string
	Method   string
	Path     string
	Header   http.Header
	Body     []byte
	Prompt   string
}

type fakeAI struct {
	server   *httptest.Server
	mutex    sync.Mutex
	replies  map[string]apiReply
	requests []apiRequest
}

func newFakeAI() *fakeAI {
	GinkgoHelper()
	api := &fakeAI{replies: map[string]apiReply{
		providerOpenAI: {Message: defaultAIResponse},
		providerClaude: {Message: defaultAIResponse},
		providerGemini: {Message: defaultAIResponse},
	}}
	api.server = httptest.NewServer(http.HandlerFunc(api.serveHTTP))
	DeferCleanup(func() {
		api.server.CloseClientConnections()
		api.server.Close()
	})
	return api
}

func (f *fakeAI) URL() string {
	return f.server.URL
}

func (f *fakeAI) setReply(provider string, reply apiReply) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.replies[provider] = reply
}

func (f *fakeAI) requestsFor(provider string) []apiRequest {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	requests := make([]apiRequest, 0)
	for _, request := range f.requests {
		if request.Provider == provider {
			requests = append(requests, request)
		}
	}
	return requests
}

func (f *fakeAI) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	provider := providerForPath(request.URL.Path)
	if provider == "" {
		http.NotFound(writer, request)
		return
	}

	body, err := readRequestBody(request)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	recorded := apiRequest{
		Provider: provider,
		Method:   request.Method,
		Path:     request.URL.RequestURI(),
		Header:   request.Header.Clone(),
		Body:     body,
		Prompt:   extractPrompt(body),
	}

	f.mutex.Lock()
	f.requests = append(f.requests, recorded)
	reply := f.replies[provider]
	f.mutex.Unlock()

	if reply.Release != nil {
		select {
		case <-reply.Release:
		case <-request.Context().Done():
			return
		}
	}
	if reply.Delay > 0 {
		select {
		case <-time.After(reply.Delay):
		case <-request.Context().Done():
			return
		}
	}

	status := reply.Status
	if status == 0 {
		status = http.StatusOK
	}
	bodyText := reply.Body
	if bodyText == "" {
		bodyText = providerResponse(provider, reply.Message)
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write([]byte(bodyText))
}

func readRequestBody(request *http.Request) ([]byte, error) {
	defer request.Body.Close()
	var value json.RawMessage
	decoder := json.NewDecoder(request.Body)
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode request body: %w", err)
	}
	return value, nil
}

func providerForPath(path string) string {
	switch {
	case strings.HasSuffix(path, "/chat/completions"):
		return providerOpenAI
	case strings.HasSuffix(path, "/v1/messages"):
		return providerClaude
	case strings.Contains(path, ":generateContent"):
		return providerGemini
	default:
		return ""
	}
}

func extractPrompt(body []byte) string {
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	if messages, ok := payload["messages"].([]any); ok && len(messages) > 0 {
		if message, ok := messages[0].(map[string]any); ok {
			return firstText(message["content"])
		}
	}
	if contents, ok := payload["contents"].([]any); ok && len(contents) > 0 {
		return firstText(contents[0])
	}
	return ""
}

func firstText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		for _, item := range typed {
			if text := firstText(item); text != "" {
				return text
			}
		}
	case map[string]any:
		if text, ok := typed["text"].(string); ok {
			return text
		}
		for _, key := range []string{"parts", "content"} {
			if text := firstText(typed[key]); text != "" {
				return text
			}
		}
	}
	return ""
}

func providerResponse(provider, message string) string {
	encodedMessage, _ := json.Marshal(message)
	switch provider {
	case providerOpenAI:
		return fmt.Sprintf(`{
  "id":"chatcmpl-e2e",
  "object":"chat.completion",
  "created":1,
  "model":"e2e-openai-model",
  "choices":[{"index":0,"message":{"role":"assistant","content":%s},"finish_reason":"stop"}],
  "usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
}`, encodedMessage)
	case providerClaude:
		return fmt.Sprintf(`{
  "id":"msg_e2e",
  "type":"message",
  "role":"assistant",
  "model":"e2e-claude-model",
  "content":[{"type":"text","text":%s}],
  "stop_reason":"end_turn",
  "stop_sequence":null,
  "usage":{"input_tokens":1,"output_tokens":1}
}`, encodedMessage)
	case providerGemini:
		return fmt.Sprintf(`{
  "candidates":[{
    "content":{"role":"model","parts":[{"text":%s}]},
    "finishReason":"STOP",
    "index":0
  }],
  "usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}
}`, encodedMessage)
	default:
		return `{}`
	}
}
