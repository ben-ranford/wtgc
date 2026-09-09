// Package provider obtains immutable merge proofs from explicitly selected hosts.
package provider

import (
	"context"
	"time"
)

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
