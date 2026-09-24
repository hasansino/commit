package commit

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestNativeRemoteDispatchError_IsTypedAfterDispatch(t *testing.T) {
	cause := &gitCommandError{
		args:       []string{"push"},
		cause:      context.DeadlineExceeded,
		dispatched: true,
	}
	err := nativeRemoteDispatchError(cause, "branch push")
	var outcome *IndeterminateRemoteOutcomeError
	if !errors.As(err, &outcome) {
		t.Fatalf("nativeRemoteDispatchError() = %T %v, want IndeterminateRemoteOutcomeError", err, err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("nativeRemoteDispatchError() = %v, want wrapped context deadline", err)
	}
	if !strings.Contains(err.Error(), "remote may have updated") {
		t.Fatalf("nativeRemoteDispatchError() = %q, want indeterminate-outcome wording", err)
	}
	if outcome.Operation != "branch push" {
		t.Fatalf("IndeterminateRemoteOutcomeError.Operation = %q", outcome.Operation)
	}
	undispatched := &gitCommandError{args: []string{"push"}, cause: context.Canceled}
	if got := nativeRemoteDispatchError(undispatched, "branch push"); got != nil {
		t.Fatalf("nativeRemoteDispatchError(undispatched) = %v, want nil", got)
	}
}

func TestTagMessagePermissionsAndCleanup(t *testing.T) {
	messagePath, cleanup, err := writeNativeTagMessage("private")
	if err != nil {
		t.Fatalf("writeNativeTagMessage() error = %v", err)
	}
	info, err := os.Stat(messagePath)
	if err != nil {
		cleanup()
		t.Fatalf("stat tag message: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		cleanup()
		t.Fatalf("tag message mode = %#o, want 0600", got)
	}
	cleanup()
	if _, err := os.Stat(messagePath); !os.IsNotExist(err) {
		t.Fatalf("tag message still exists after cleanup: %v", err)
	}
}
