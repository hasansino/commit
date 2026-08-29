package cmd

import (
	"context"
	"testing"

	"github.com/spf13/viper"

	"github.com/hasansino/commit/internal/cmdutil"
)

func TestNewCommitCommandJiraDefaults(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	ctx := context.Background()
	command := NewCommitCommand(ctx, cmdutil.NewFactory(ctx))

	tests := []struct {
		name string
		want string
	}{
		{name: "jira-task-position", want: "none"},
		{name: "jira-task-style", want: "plain"},
	}

	for _, tt := range tests {
		flag := command.Flags().Lookup(tt.name)
		if flag == nil {
			t.Fatalf("flag %q not found", tt.name)
		}
		if flag.DefValue != tt.want {
			t.Errorf("flag %q default = %q, want %q", tt.name, flag.DefValue, tt.want)
		}
	}
}
