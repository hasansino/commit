package models

import (
	"crypto/sha256"
	"fmt"
	"os"
)

// RawIndexState identifies the exact contents and metadata of a Git index.
type RawIndexState struct {
	Exists bool
	Mode   os.FileMode
	Digest [sha256.Size]byte
}

// HeadSnapshot identifies the symbolic reference and object at HEAD.
type HeadSnapshot struct {
	Name string
	Hash string
}

// StagingSessionPhase describes the lifecycle of a staging session.
type StagingSessionPhase uint8

const (
	StagingSessionPrepared StagingSessionPhase = iota
	StagingSessionNoChanges
	StagingSessionCommitting
	StagingSessionCommitted
	StagingSessionFailed
	StagingSessionRecoveryRequired
	StagingSessionFinished
)

// StagingSessionState owns a private index for one commit invocation.
type StagingSessionState struct {
	Files             []string
	IndexPath         string
	PrivateIndexPath  string
	IndexMode         os.FileMode
	IndexState        []byte
	RawIndexState     RawIndexState
	PrivateIndexState []byte
	BaseTree          string
	ExpectedHead      HeadSnapshot
	UsingExisting     bool
	Phase             StagingSessionPhase
	Closed            bool
	LeaseHeld         bool
	Recovery          *CommitRecoveryError
}

// UsesExistingStaging reports whether the session started from an already
// staged index rather than staging working-tree changes itself.
func (s *StagingSessionState) UsesExistingStaging() bool {
	return s != nil && s.UsingExisting
}

// CommitResult identifies the commit created by native Git and records its
// final message after commit-msg hooks have run.
type CommitResult struct {
	Hash    string
	Message string
}

// CommitRecoveryError means automatic rollback or index promotion would risk
// losing repository state. The retained paths are available for inspection
// and manual recovery.
type CommitRecoveryError struct {
	StandardIndexLockPath string
	PrivateIndexPath      string
	ExpectedHEAD          string
	CurrentHEAD           string
	Action                string
	Cause                 error
}

func (e *CommitRecoveryError) Error() string {
	return fmt.Sprintf(
		"commit outcome requires recovery: %s (index lock: %s; private index: %s): %v",
		e.Action,
		e.StandardIndexLockPath,
		e.PrivateIndexPath,
		e.Cause,
	)
}

func (e *CommitRecoveryError) Unwrap() error {
	return e.Cause
}
