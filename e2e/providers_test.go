package e2e_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("AI provider boundaries", func() {
	type providerExpectation struct {
		Name         string
		Path         string
		Header       string
		HeaderValue  string
		BodyFragment string
	}

	DescribeTable("uses each configured provider through its public HTTP contract",
		func(ctx SpecContext, expected providerExpectation) {
			repository := newRepository()
			repository.append("tracked.txt", "provider-specific change\n")
			api := newFakeAI()
			api.setReply(expected.Name, apiReply{Message: "fix(api): use " + expected.Name})
			options := runOptions{
				API:          api,
				Providers:    []string{expected.Name},
				GlobalConfig: repository.GlobalConfig,
			}

			result := runCLI(
				ctx,
				repository.Path,
				options,
				"--auto",
				"--dry-run",
				"--providers="+strings.ToUpper(expected.Name),
				"--prompt=provider prompt: {branch} | {files}",
			)

			Expect(result.ExitCode).To(Equal(0))
			Expect(result.Output()).To(ContainSubstring("fix(api): use " + expected.Name))
			requests := api.requestsFor(expected.Name)
			Expect(requests).To(HaveLen(1))
			Expect(requests[0].Method).To(Equal(http.MethodPost))
			Expect(requests[0].Path).To(ContainSubstring(expected.Path))
			Expect(requests[0].Header.Get(expected.Header)).To(Equal(expected.HeaderValue))
			Expect(string(requests[0].Body)).To(ContainSubstring(expected.BodyFragment))
			Expect(requests[0].Prompt).To(Equal("provider prompt: master | tracked.txt"))
			Expect(repository.head()).To(Equal(repository.InitialHead))
		},
		Entry("OpenAI", providerExpectation{
			Name:         providerOpenAI,
			Path:         "/v1/chat/completions",
			Header:       "Authorization",
			HeaderValue:  "Bearer e2e-openai-key",
			BodyFragment: `"model":"e2e-openai-model"`,
		}),
		Entry("Claude", providerExpectation{
			Name:         providerClaude,
			Path:         "/v1/messages",
			Header:       "X-Api-Key",
			HeaderValue:  "e2e-claude-key",
			BodyFragment: `"model":"e2e-claude-model"`,
		}),
		Entry("Gemini", providerExpectation{
			Name:         providerGemini,
			Path:         "/v1beta/models/e2e-gemini-model:generateContent",
			Header:       "X-Goog-Api-Key",
			HeaderValue:  "e2e-gemini-key",
			BodyFragment: `"contents"`,
		}),
	)

	It("calls every configured provider when no filter is supplied", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "fan out to all providers\n")
		api := newFakeAI()
		options := runOptions{
			API:          api,
			Providers:    []string{providerOpenAI, providerClaude, providerGemini},
			GlobalConfig: repository.GlobalConfig,
		}

		result := runCLI(ctx, repository.Path, options, "--auto", "--dry-run")

		Expect(result.ExitCode).To(Equal(0))
		for _, provider := range options.Providers {
			Expect(api.requestsFor(provider)).To(HaveLen(1), "provider %s", provider)
		}
	})

	It("continues when one of several providers fails", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "partial provider success\n")
		api := newFakeAI()
		api.setReply(providerOpenAI, apiReply{
			Status: http.StatusBadRequest,
			Body:   `{"error":{"message":"synthetic rejection","type":"invalid_request_error"}}`,
		})
		api.setReply(providerClaude, apiReply{Message: "fix(core): use surviving provider"})
		options := runOptions{
			API:          api,
			Providers:    []string{providerOpenAI, providerClaude},
			GlobalConfig: repository.GlobalConfig,
		}

		result := runCLI(
			ctx,
			repository.Path,
			options,
			"--auto",
			"--dry-run",
			"--providers=openai,claude",
		)

		Expect(result.ExitCode).To(Equal(0))
		Expect(result.Output()).To(ContainSubstring("fix(core): use surviving provider"))
		Expect(api.requestsFor(providerOpenAI)).NotTo(BeEmpty())
		Expect(api.requestsFor(providerClaude)).To(HaveLen(1))
	})

	It("rejects a requested provider that has no configured credential", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "unavailable provider\n")
		beforeIndex := repository.indexBytes()
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(ctx, repository.Path, options, "--auto", "--providers=claude")

		Expect(result.ExitCode).To(Equal(1))
		Expect(result.Output()).To(ContainSubstring("no AI providers available"))
		Expect(api.requestsFor(providerOpenAI)).To(BeEmpty())
		Expect(repository.indexBytes()).To(Equal(beforeIndex))
		Expect(repository.head()).To(Equal(repository.InitialHead))
	})

	DescribeTable("fails closed for unusable provider responses",
		func(ctx SpecContext, reply apiReply) {
			repository := newRepository()
			repository.append("tracked.txt", "provider should fail\n")
			beforeIndex := repository.indexBytes()
			api := newFakeAI()
			api.setReply(providerOpenAI, reply)
			options := openAIOptions(api)
			options.GlobalConfig = repository.GlobalConfig

			result := runCLI(ctx, repository.Path, options, "--auto", "--providers=openai")

			Expect(result.ExitCode).To(Equal(1))
			Expect(result.Output()).To(ContainSubstring("no valid suggestions available for auto-commit"))
			Expect(api.requestsFor(providerOpenAI)).NotTo(BeEmpty())
			Expect(repository.indexBytes()).To(Equal(beforeIndex))
			Expect(repository.head()).To(Equal(repository.InitialHead))
		},
		Entry("an HTTP error", apiReply{
			Status: http.StatusBadRequest,
			Body:   `{"error":{"message":"synthetic rejection","type":"invalid_request_error"}}`,
		}),
		Entry("malformed JSON", apiReply{Body: `{not-json`}),
		Entry("empty content", apiReply{Message: ""}),
		Entry("an invalid finish reason", apiReply{Body: `{
  "id":"chatcmpl-e2e",
  "object":"chat.completion",
  "created":1,
  "model":"e2e-openai-model",
  "choices":[{"index":0,"message":{"role":"assistant","content":"ignored"},"finish_reason":"length"}]
}`}),
	)

	It("enforces the configured provider timeout and restores staging", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "slow provider\n")
		beforeIndex := repository.indexBytes()
		api := newFakeAI()
		api.setReply(providerOpenAI, apiReply{Message: "fix: too late", Delay: 10 * time.Second})
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		started := time.Now()
		result := runCLI(
			ctx,
			repository.Path,
			options,
			"--auto",
			"--providers=openai",
			"--timeout=100ms",
		)

		Expect(result.ExitCode).To(Equal(1))
		Expect(time.Since(started)).To(BeNumerically("<", 8*time.Second))
		Expect(result.Output()).To(ContainSubstring("no valid suggestions available for auto-commit"))
		Expect(api.requestsFor(providerOpenAI)).To(HaveLen(1))
		Expect(repository.indexBytes()).To(Equal(beforeIndex))
		Expect(repository.head()).To(Equal(repository.InitialHead))
	})
})

var _ = Describe("Prompt construction", func() {
	It("expands branch, file, and diff variables in a custom prompt", func(ctx SpecContext) {
		repository := newRepository()
		repository.git("switch", "-c", "feature/prompt-contract")
		repository.append("tracked.txt", "custom prompt marker\n")
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(
			ctx,
			repository.Path,
			options,
			"--auto",
			"--dry-run",
			"--providers=openai",
			"--prompt=branch={branch}\nfiles={files}\ndiff={diff}",
		)

		Expect(result.ExitCode).To(Equal(0))
		requests := api.requestsFor(providerOpenAI)
		Expect(requests).To(HaveLen(1))
		Expect(requests[0].Prompt).To(HavePrefix("branch=feature/prompt-contract\nfiles=tracked.txt\ndiff="))
		Expect(requests[0].Prompt).To(ContainSubstring("custom prompt marker"))
	})

	DescribeTable(
		"selects the documented default message format",
		func(ctx SpecContext, extraArgument, expected, unexpected string) {
			repository := newRepository()
			repository.append("tracked.txt", "format change\n")
			api := newFakeAI()
			options := openAIOptions(api)
			options.GlobalConfig = repository.GlobalConfig
			arguments := []string{"--auto", "--dry-run", "--providers=openai"}
			if extraArgument != "" {
				arguments = append(arguments, extraArgument)
			}

			result := runCLI(ctx, repository.Path, options, arguments...)

			Expect(result.ExitCode).To(Equal(0))
			prompt := api.requestsFor(providerOpenAI)[0].Prompt
			Expect(prompt).To(ContainSubstring(expected))
			Expect(prompt).NotTo(ContainSubstring(unexpected))
		},
		Entry("single-line", "", "Use STRICTLY single-line commit message", "Prefer single-line commit message"),
		Entry(
			"multi-line",
			"--multi-line",
			"Prefer single-line commit message",
			"Use STRICTLY single-line commit message",
		),
	)

	It("caps the diff sent to a provider", func(ctx SpecContext) {
		repository := newRepository()
		repository.write("large.txt", strings.Repeat("a long changed line for truncation\n", 300))
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(
			ctx,
			repository.Path,
			options,
			"--auto",
			"--dry-run",
			"--providers=openai",
			"--prompt={diff}",
			"--max-diff-size-bytes=512",
		)

		Expect(result.ExitCode).To(Equal(0))
		requests := api.requestsFor(providerOpenAI)
		Expect(requests).To(HaveLen(1))
		Expect(len(requests[0].Prompt)).To(BeNumerically("<=", 512))
		Expect(requests[0].Prompt).To(ContainSubstring("Diff excerpts"))
		Expect(requests[0].Prompt).To(ContainSubstring("+a long changed line for truncation\n"))
	})

	DescribeTable("rejects unusable diff limits before calling AI",
		func(ctx SpecContext, limit, expectedError string) {
			repository := newRepository()
			repository.append("tracked.txt", "must not commit without a useful diff\n")
			beforeIndex := repository.indexBytes()
			beforeStatus := repository.status()
			api := newFakeAI()
			options := openAIOptions(api)
			options.GlobalConfig = repository.GlobalConfig
			tracePath := filepath.Join(GinkgoT().TempDir(), "git-trace.json")
			options.Environment = map[string]string{"GIT_TRACE2_EVENT": tracePath}

			result := runCLI(ctx, repository.Path, options,
				"--auto", "--providers=openai", "--max-diff-size-bytes="+limit)

			Expect(result.ExitCode).To(Equal(1))
			Expect(result.Output()).To(ContainSubstring(expectedError))
			Expect(api.requestsFor(providerOpenAI)).To(BeEmpty())
			Expect(repository.head()).To(Equal(repository.InitialHead))
			Expect(repository.indexBytes()).To(Equal(beforeIndex))
			Expect(repository.status()).To(Equal(beforeStatus))
			Expect(privateStagingFiles(repository)).To(BeEmpty())
			if limit == "1" {
				trace, err := os.ReadFile(tracePath)
				Expect(err).NotTo(HaveOccurred())
				zeroContextDiffs := 0
				for _, line := range strings.Split(strings.TrimSpace(string(trace)), "\n") {
					var event struct {
						Event string   `json:"event"`
						Argv  []string `json:"argv"`
					}
					Expect(json.Unmarshal([]byte(line), &event)).To(Succeed())
					if event.Event == "start" && slices.Contains(event.Argv, "diff") &&
						slices.Contains(event.Argv, "--cached") && slices.Contains(event.Argv, "-U0") {
						zeroContextDiffs++
					}
				}
				Expect(zeroContextDiffs).To(Equal(1), "the smallest diff should only be requested once")
			}
		},
		Entry("zero", "0", "max diff size bytes must be greater than zero"),
		Entry("too small for a file summary", "1", "too small to describe staged changes"),
	)

	DescribeTable("sends both sides of a large replacement and commits the exact snapshot",
		func(ctx SpecContext, staged bool) {
			repository := newRepository()
			oldContent := strings.Repeat(
				"old implementation with enough content to exceed the default diff limit\n",
				3000,
			)
			newContent := strings.Repeat(
				"new implementation with enough content to exceed the default diff limit\n",
				3000,
			)
			repository.write("tracked.txt", oldContent)
			repository.git("add", "tracked.txt")
			repository.git("commit", "--no-gpg-sign", "-m", "test: seed large file")
			repository.write("tracked.txt", newContent)
			if staged {
				repository.git("add", "tracked.txt")
				repository.append("tracked.txt", "unstaged-marker-must-stay-out\n")
				repository.write("unrelated.txt", "untracked-marker-must-stay-out\n")
			}
			api := newFakeAI()
			options := openAIOptions(api)
			options.GlobalConfig = repository.GlobalConfig

			result := runCLI(ctx, repository.Path, options,
				"--auto", "--providers=openai", "--prompt={diff}")

			Expect(result.ExitCode).To(Equal(0), result.Output())
			requests := api.requestsFor(providerOpenAI)
			Expect(requests).To(HaveLen(1))
			prompt := requests[0].Prompt
			Expect(len(prompt)).To(BeNumerically("<=", 64*1024))
			Expect(prompt).To(ContainSubstring("Diff excerpts"))
			Expect(prompt).To(ContainSubstring("-old implementation"))
			Expect(prompt).To(ContainSubstring("+new implementation"))
			Expect(prompt).To(ContainSubstring("File totals: +3000 -3000 lines"))
			Expect(prompt).To(ContainSubstring("Omitted lines:"))
			Expect(prompt).NotTo(ContainSubstring("unstaged-marker"))
			Expect(prompt).NotTo(ContainSubstring("untracked-marker"))
			Expect(repository.git("show", "HEAD:tracked.txt")).To(Equal(newContent))
			Expect(repository.git("diff", "--cached", "--name-only")).To(BeEmpty())
			if staged {
				Expect(repository.read("tracked.txt")).To(Equal(newContent + "unstaged-marker-must-stay-out\n"))
				Expect(repository.status()).To(ContainSubstring("?? unrelated.txt"))
			} else {
				Expect(repository.status()).To(BeEmpty())
			}
		},
		Entry("existing partial staging", true),
		Entry("automatic staging", false),
	)
})
