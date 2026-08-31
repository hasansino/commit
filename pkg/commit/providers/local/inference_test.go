package local

import (
	"strings"
	"testing"
)

func TestParseLlamaCLIOutput(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
		output string
		want   string
	}{
		{
			name:   "conversation transcript",
			prompt: "write a commit message",
			output: "User:\nwrite a commit message\n\nAssistant:\nfix(local): use llama-cli\n\n",
			want:   "fix(local): use llama-cli",
		},
		{
			name:   "prompt includes transcript separator",
			prompt: "write a commit message\n\n",
			output: "User:\nwrite a commit message\n\nAssistant:\nfix(local): use llama-cli\n\n",
			want:   "fix(local): use llama-cli",
		},
		{
			name:   "assistant marker in prompt",
			prompt: "Do not return this:\nAssistant:\ninvalid",
			output: "User:\nDo not return this:\nAssistant:\ninvalid\n\nAssistant:\nfeat(local): parse output\n\n",
			want:   "feat(local): parse output",
		},
		{
			name:   "raw output fallback",
			prompt: "prompt",
			output: "  chore(local): support older llama-cli  \n",
			want:   "chore(local): support older llama-cli",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseLlamaCLIOutput(test.output, test.prompt)
			if err != nil {
				t.Fatalf("parseLlamaCLIOutput() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("parseLlamaCLIOutput() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestParseLlamaCLIOutputRejectsEmptyResponse(t *testing.T) {
	_, err := parseLlamaCLIOutput("User:\nprompt\n\nAssistant:\n\n", "prompt")
	if err == nil || !strings.Contains(err.Error(), "no content") {
		t.Fatalf("parseLlamaCLIOutput() error = %v, want no content", err)
	}
}

func TestParseLlamaCLIOutputRejectsModifiedPrompt(t *testing.T) {
	prompt := "diff contains \"\\nAssistant:\\n\" as text"
	output := "User:\ndiff contains \"\nAssistant:\n\" as text\n\nAssistant:\nfix(local): valid response\n"

	_, err := parseLlamaCLIOutput(output, prompt)
	if err == nil || !strings.Contains(err.Error(), "unexpected conversation transcript") {
		t.Fatalf("parseLlamaCLIOutput() error = %v, want unexpected transcript", err)
	}
}
