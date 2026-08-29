package e2e_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Generated commit messages", func() {
	It("removes an outer Markdown fence and preserves a multiline body", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "multiline change\n")
		api := newFakeAI()
		api.setReply(providerOpenAI, apiReply{Message: "\n\n```gitcommit\n" +
			"feat(core): preserve useful details\n\n" +
			"- add the first behavior\n" +
			"- retain the second behavior\n" +
			"```\n\n"})
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(ctx, repository.Path, options, "--auto", "--providers=openai", "--multi-line")

		Expect(result.ExitCode).To(Equal(0))
		Expect(repository.commitMessage()).To(Equal("feat(core): preserve useful details\n\n" +
			"- add the first behavior\n" +
			"- retain the second behavior"))
	})

	DescribeTable("adds a Jira issue from the branch to the selected message",
		func(
			ctx SpecContext,
			branch string,
			position string,
			style string,
			message string,
			expected string,
		) {
			repository := newRepository()
			repository.git("switch", "-c", branch)
			repository.append("tracked.txt", "Jira change\n")
			api := newFakeAI()
			api.setReply(providerOpenAI, apiReply{Message: message})
			options := openAIOptions(api)
			options.GlobalConfig = repository.GlobalConfig

			result := runCLI(
				ctx,
				repository.Path,
				options,
				"--auto",
				"--providers=openai",
				"--jira-task-position="+position,
				"--jira-task-style="+style,
			)

			Expect(result.ExitCode).To(Equal(0))
			Expect(repository.commitMessage()).To(Equal(expected))
		},
		Entry(
			"as a bracketed prefix",
			"feature/APP-123-add-search",
			"prefix",
			"brackets",
			"feat(api): add search",
			"[APP-123] feat(api): add search",
		),
		Entry(
			"after a conventional-commit prefix",
			"bugfix/APP-124-search-crash",
			"infix",
			"plain",
			"fix(api): prevent search crash",
			"fix(api): APP-124 prevent search crash",
		),
		Entry(
			"as a parenthesized suffix",
			"hotfix/OPS-9-production",
			"suffix",
			"parens",
			"fix: restore service",
			"fix: restore service (OPS-9)",
		),
		Entry(
			"with the prefix colon style",
			"chore/DOC-77-refresh-guide",
			"PREFIX",
			"PLAIN-COLON",
			"docs(readme): refresh guide",
			"DOC-77: docs(readme): refresh guide",
		),
		Entry(
			"on only the first line of a multiline message",
			"team/APP-125-more-context",
			"infix",
			"brackets",
			"feat(core): add context\n\n- preserve this body",
			"feat(core): [APP-125] add context\n\n- preserve this body",
		),
		Entry(
			"without duplicating an issue already in the message",
			"feature/APP-126-deduplicate",
			"suffix",
			"plain",
			"feat: handle APP-126 once",
			"feat: handle APP-126 once",
		),
		Entry(
			"not at all when disabled",
			"feature/APP-127-disabled",
			"none",
			"brackets",
			"feat: leave the message alone",
			"feat: leave the message alone",
		),
	)
})
