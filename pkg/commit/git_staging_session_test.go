package commit

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hasansino/commit/pkg/commit/models"
)

func TestFinishStagingRetainsRecoveryArtifactsAndReleasesSession(t *testing.T) {
	directory := t.TempDir()
	indexPath, privatePath := filepath.Join(directory, "index"), filepath.Join(directory, "private-index")
	paths := []string{indexPath + ".lock", privatePath, privatePath + ".lock"}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("recovery data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	recovery := &models.CommitRecoveryError{Action: "inspect retained state"}
	session := &models.StagingSessionState{
		IndexPath: indexPath, PrivateIndexPath: privatePath, Phase: models.StagingSessionRecoveryRequired,
		LeaseHeld: true, Recovery: recovery,
	}
	gitOps := &gitOperations{activeSession: session}
	gitOps.sessionLease.Lock()
	if err := gitOps.FinishStaging(session); !errors.Is(err, recovery) {
		t.Fatalf("FinishStaging() error = %v, want retained recovery", err)
	}
	for _, path := range paths {
		contents, err := os.ReadFile(path)
		if err != nil || string(contents) != "recovery data" {
			t.Fatalf("recovery artifact %q = %q, error = %v", path, contents, err)
		}
	}
	if !session.Closed || session.LeaseHeld || !gitOps.sessionLease.TryLock() {
		t.Fatal("FinishStaging() did not close and release the recovery session")
	}
	gitOps.sessionLease.Unlock()
}

func TestBeginStagingRejectsOverlappingSession(t *testing.T) {
	gitOps := &gitOperations{}
	gitOps.sessionLease.Lock()
	defer gitOps.sessionLease.Unlock()

	if _, err := gitOps.BeginStaging(context.Background(), nil, nil, false); err == nil ||
		!strings.Contains(err.Error(), "already active") {
		t.Fatalf("BeginStaging() error = %v, want active-session rejection before invoking Git", err)
	}
}

func TestFinishStagingCleansUpAndReleasesSession(t *testing.T) {
	for _, test := range []struct {
		name  string
		phase models.StagingSessionPhase
	}{
		{name: "prepared", phase: models.StagingSessionPrepared},
		{name: "no changes", phase: models.StagingSessionNoChanges},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			indexPath := filepath.Join(directory, "index")
			originalIndex := []byte("original index bytes")
			if err := os.WriteFile(indexPath, originalIndex, 0o600); err != nil {
				t.Fatal(err)
			}
			session := &models.StagingSessionState{
				IndexPath: indexPath,
				Phase:     test.phase,
				Closed:    test.phase == models.StagingSessionNoChanges,
				LeaseHeld: true,
			}
			var privatePaths []string
			if test.phase == models.StagingSessionPrepared {
				session.PrivateIndexPath = filepath.Join(directory, "private-index")
				privatePaths = []string{session.PrivateIndexPath, session.PrivateIndexPath + ".lock"}
				for _, path := range privatePaths {
					if err := os.WriteFile(path, []byte("temporary index bytes"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			gitOps := &gitOperations{activeSession: session}
			gitOps.sessionLease.Lock()

			if err := gitOps.FinishStaging(session); err != nil {
				t.Fatalf("FinishStaging() error = %v", err)
			}
			if !session.Closed || session.LeaseHeld || session.Phase != models.StagingSessionFinished {
				t.Fatalf("finished session = %+v, want closed, finished, and released", session)
			}
			for _, path := range privatePaths {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("private artifact %q still exists after FinishStaging(): %v", path, err)
				}
			}
			if err := gitOps.FinishStaging(session); err != nil {
				t.Fatalf("second FinishStaging() error = %v, want terminal no-op", err)
			}
			index, err := os.ReadFile(indexPath)
			if err != nil || !bytes.Equal(index, originalIndex) {
				t.Fatalf("real index = %q, error = %v, want original bytes unchanged", index, err)
			}
			if !gitOps.sessionLease.TryLock() {
				t.Fatal("FinishStaging() did not release the session lease")
			}
			gitOps.sessionLease.Unlock()
		})
	}
}
