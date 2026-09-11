// Package provider obtains immutable merge proofs from explicitly selected hosts.
package provider

import (
	"context"
	"errors"
	"time"
)

// ErrNoExactProof means GitHub responded successfully but did not provide a
// complete immutable merge proof for the requested branch tip.
var ErrNoExactProof = errors.New("no exact merged pull request proof found")

// UnavailableError distinguishes an operational provider failure from a
// successful response that simply lacks proof.
type UnavailableError struct{ Cause error }

func (e *UnavailableError) Error() string { return e.Cause.Error() }
func (e *UnavailableError) Unwrap() error { return e.Cause }

// Unavailable marks an operational provider failure for outcome-aware callers.
func Unavailable(err error) error { return &UnavailableError{Cause: err} }

// PullRequest is the immutable information required to prove a merged pull request.
type PullRequest struct {
	HeadRemote     string
	BaseRemote     string
	Number         int
	URL            string
	MergedAt       time.Time
	HeadSHA        string
	HeadOwner      string
	HeadRepo       string
	HeadRef        string
	BaseOwner      string
	BaseRepo       string
	BaseRef        string
	MergeCommitSHA string
}

// Query identifies one exact source branch and target branch.
type Query struct {
	HeadOwner string
	HeadRepo  string
	HeadRef   string
	BaseOwner string
	BaseRepo  string
	BaseRef   string
	HeadSHA   string
}

// MergeFinder is deliberately independent from Git inspection and cleanup decisions.
type MergeFinder interface {
	FindMerged(context.Context, Query) (PullRequest, error)
}
