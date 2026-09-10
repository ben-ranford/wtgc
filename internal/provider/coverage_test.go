package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCoverageQueryAndPaginationBoundaries(t *testing.T) {
	if err := validQuery(Query{}); err == nil {
		t.Fatal("empty query accepted")
	}
	q := Query{HeadOwner: "o", HeadRepo: "r", HeadRef: "f", HeadSHA: "s", BaseOwner: "b", BaseRepo: "x", BaseRef: "main"}
	calls := 0
	client := NewGitHub(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("[" + strings.Repeat(`{},`, 99) + `{}]`))}, nil
	})})
	if _, err := client.FindMerged(context.Background(), q); err == nil || !strings.Contains(err.Error(), "pagination limit") || calls != 10 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestCoveragePageHeadersAndResponseFailures(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "fallback")
	q := Query{HeadOwner: "source owner", HeadRepo: "source/repo", HeadRef: "f", HeadSHA: "s", BaseOwner: "base owner", BaseRepo: "base/repo", BaseRef: "main"}
	client := NewGitHub(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer fallback" || r.Header.Get("Accept") == "" || r.Header.Get("X-GitHub-Api-Version") == "" {
			t.Fatalf("headers=%v", r.Header)
		}
		if r.URL.Query().Get("page") != "3" || r.URL.EscapedPath() != "/repos/base%20owner/base%2Frepo/pulls" {
			t.Fatalf("url=%s", r.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("[]"))}, nil
	})})
	if _, err := client.page(context.Background(), q, 3); err != nil {
		t.Fatalf("page: %v", err)
	}

	failed := NewGitHub(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("transport") })})
	if _, err := failed.page(context.Background(), q, 1); err == nil || !strings.Contains(err.Error(), "query failed") {
		t.Fatalf("page transport err=%v", err)
	}
	for _, body := range []io.ReadCloser{
		failingBody{readErr: errors.New("read")},
		failingBody{data: "error", closeErr: errors.New("close")},
	} {
		if _, err := decodePulls(&http.Response{StatusCode: http.StatusBadGateway, Body: body}); err == nil || !strings.Contains(err.Error(), "could not be read") {
			t.Fatalf("status body err=%v", err)
		}
	}
}

func TestCoverageFindMergedPropagatesPageFailure(t *testing.T) {
	q := Query{HeadOwner: "o", HeadRepo: "r", HeadRef: "f", HeadSHA: "s", BaseOwner: "b", BaseRepo: "x", BaseRef: "main"}
	client := NewGitHub(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })})
	if _, err := client.FindMerged(context.Background(), q); err == nil || !strings.Contains(err.Error(), "query failed") {
		t.Fatalf("FindMerged error=%v", err)
	}
	if _, err := client.FindMerged(context.Background(), Query{}); err == nil || !strings.Contains(err.Error(), "requires complete") {
		t.Fatalf("invalid query error=%v", err)
	}
}
