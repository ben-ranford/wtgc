package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (r roundTrip) RoundTrip(q *http.Request) (*http.Response, error) { return r(q) }
func TestGitHubAcceptsOnlyExactIdentity(t *testing.T) {
	c := NewGitHub(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.github.com" || r.URL.Query().Get("head") != "fork:feature" {
			t.Fatalf("url=%s", r.URL)
		}
		body := `[{"state":"closed","merged_at":"2026-01-02T03:04:05Z","merge_commit_sha":"merge","head":{"sha":"oid","ref":"feature","repo":{"name":"source","owner":{"login":"fork"}}},"base":{"ref":"main","repo":{"name":"target","owner":{"login":"base"}}}}]`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})})
	p, err := c.FindMerged(context.Background(), Query{HeadOwner: "fork", HeadRepo: "source", HeadRef: "feature", HeadSHA: "oid", BaseOwner: "base", BaseRepo: "target", BaseRef: "main"})
	if err != nil || p.MergeCommitSHA != "merge" {
		t.Fatalf("proof=%+v err=%v", p, err)
	}
}

func TestGitHubAcceptsCanonicalRepositoryCasingOnly(t *testing.T) {
	c := NewGitHub(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		body := `[{"state":"closed","merged_at":"2026-01-02T03:04:05Z","merge_commit_sha":"merge","head":{"sha":"oid","ref":"feature","repo":{"name":"Source","owner":{"login":"Fork"}}},"base":{"ref":"main","repo":{"name":"Target","owner":{"login":"Base"}}}}]`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})})
	if _, err := c.FindMerged(context.Background(), Query{HeadOwner: "fork", HeadRepo: "source", HeadRef: "feature", HeadSHA: "oid", BaseOwner: "base", BaseRepo: "target", BaseRef: "main"}); err != nil {
		t.Fatalf("canonical repository casing rejected: %v", err)
	}
}
func TestGitHubRejectsWrongOIDAndOversizedResponse(t *testing.T) {
	c := NewGitHub(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`[{"state":"closed","number":7,"merged_at":"2026-01-02T03:04:05Z","merge_commit_sha":"m","head":{"sha":"wrong","ref":"f","repo":{"name":"r","owner":{"login":"o"}}},"base":{"ref":"b","repo":{"name":"x","owner":{"login":"y"}}}}]`))}, nil
	})})
	_, err := c.FindMerged(context.Background(), Query{HeadOwner: "o", HeadRepo: "r", HeadRef: "f", HeadSHA: "right", BaseOwner: "y", BaseRepo: "x", BaseRef: "b"})
	if err == nil {
		t.Fatal("wrong OID accepted")
	}
}

func TestGitHubRejectsIncompleteOrMismatchedMergedProof(t *testing.T) {
	query := Query{HeadOwner: "fork", HeadRepo: "source", HeadRef: "feature", HeadSHA: "oid", BaseOwner: "base", BaseRepo: "target", BaseRef: "main"}
	valid := `[{"state":"closed","number":7,"merged_at":"2026-01-02T03:04:05Z","merge_commit_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","head":{"sha":"oid","ref":"feature","repo":{"name":"source","owner":{"login":"fork"}}},"base":{"ref":"main","repo":{"name":"target","owner":{"login":"base"}}}}]`
	for _, test := range []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "closed but unmerged", body: strings.Replace(valid, `"merged_at":"2026-01-02T03:04:05Z"`, `"merged_at":null`, 1), wantErr: "no exact"},
		{name: "missing pull request", body: "[]", wantErr: "no exact"},
		{name: "head owner", body: strings.Replace(valid, `"login":"fork"`, `"login":"wrong"`, 1), wantErr: "no exact"},
		{name: "head repository", body: strings.Replace(valid, `"name":"source"`, `"name":"wrong"`, 1), wantErr: "no exact"},
		{name: "head ref", body: strings.Replace(valid, `"ref":"feature"`, `"ref":"wrong"`, 1), wantErr: "no exact"},
		{name: "base owner", body: strings.Replace(valid, `"login":"base"`, `"login":"wrong"`, 1), wantErr: "no exact"},
		{name: "base repository", body: strings.Replace(valid, `"name":"target"`, `"name":"wrong"`, 1), wantErr: "no exact"},
		{name: "base ref", body: strings.Replace(valid, `"ref":"main"`, `"ref":"wrong"`, 1), wantErr: "no exact"},
		{name: "missing merge commit", body: strings.Replace(valid, `"merge_commit_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`, `"merge_commit_sha":""`, 1), wantErr: "no merge commit"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := NewGitHub(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})})
			if _, err := client.FindMerged(context.Background(), query); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("err=%v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestGitHubRejectsTransportStatusAndMalformedResponses(t *testing.T) {
	q := Query{HeadOwner: "o", HeadRepo: "r", HeadRef: "f", HeadSHA: "s", BaseOwner: "b", BaseRepo: "x", BaseRef: "main"}
	for _, response := range []struct {
		status int
		body   string
	}{{401, "no"}, {429, "rate limited"}, {200, "{"}} {
		c := NewGitHub(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: response.status, Body: io.NopCloser(strings.NewReader(response.body)), Header: make(http.Header)}, nil
		})})
		if _, err := c.FindMerged(context.Background(), q); err == nil {
			t.Fatalf("response %+v accepted", response)
		}
	}
}

func TestGitHubRejectsTransportFailureWithoutLeakingDetails(t *testing.T) {
	client := NewGitHub(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("offline secret-token")
	})})
	_, err := client.FindMerged(context.Background(), Query{HeadOwner: "o", HeadRepo: "r", HeadRef: "f", HeadSHA: "s", BaseOwner: "b", BaseRepo: "x", BaseRef: "main"})
	if err == nil || !strings.Contains(err.Error(), "query failed") || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("err=%v", err)
	}
}

func TestGitHubPaginatesBoundedly(t *testing.T) {
	calls := 0
	q := Query{HeadOwner: "o", HeadRepo: "r", HeadRef: "f", HeadSHA: "s", BaseOwner: "b", BaseRepo: "x", BaseRef: "main"}
	c := NewGitHub(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		body := "[]"
		if r.URL.Query().Get("page") == "2" {
			body = `[{"state":"closed","merged_at":"2026-01-02T03:04:05Z","merge_commit_sha":"m","head":{"sha":"s","ref":"f","repo":{"name":"r","owner":{"login":"o"}}},"base":{"ref":"main","repo":{"name":"x","owner":{"login":"b"}}}}]`
		}
		if calls == 1 {
			body = "[" + strings.Repeat(`{},`, 99) + `{}]`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})})
	if _, err := c.FindMerged(context.Background(), q); err != nil || calls != 2 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestGitHubRejectsOversizedAndCancelledResponsesWithoutLeakingToken(t *testing.T) {
	t.Setenv("GH_TOKEN", "secret-token")
	q := Query{HeadOwner: "o", HeadRepo: "r", HeadRef: "f", HeadSHA: "s", BaseOwner: "b", BaseRepo: "x", BaseRef: "main"}
	c := NewGitHub(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer secret-token" {
			t.Fatal("token header missing")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxResponseBytes+1))), Header: make(http.Header)}, nil
	})})
	if _, err := c.FindMerged(context.Background(), q); err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("oversize err=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.FindMerged(ctx, q); err == nil {
		t.Fatal("cancelled request accepted")
	}
}

func TestDefaultGitHubClientRefusesCrossHostRedirectBeforeForwardingToken(t *testing.T) {
	t.Setenv("GH_TOKEN", "secret-token")
	github := NewGitHub(nil)
	client, ok := github.http.(*http.Client)
	if !ok {
		t.Fatal("default GitHub client is not an http.Client")
	}
	calls := 0
	client.Transport = roundTrip(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Host != "api.github.com" || request.Header.Get("Authorization") != "Bearer secret-token" {
			t.Fatalf("initial request=%s authorization=%q", request.URL, request.Header.Get("Authorization"))
		}
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://untrusted.example/pulls"}}, Body: io.NopCloser(strings.NewReader("redirect"))}, nil
	})
	_, err := github.FindMerged(context.Background(), Query{HeadOwner: "o", HeadRepo: "r", HeadRef: "f", HeadSHA: "s", BaseOwner: "b", BaseRepo: "x", BaseRef: "main"})
	if err == nil || calls != 1 {
		t.Fatalf("redirect err=%v calls=%d", err, calls)
	}
}

func TestGitHubRejectsBodyReadAndCloseFailures(t *testing.T) {
	q := Query{HeadOwner: "o", HeadRepo: "r", HeadRef: "f", HeadSHA: "s", BaseOwner: "b", BaseRepo: "x", BaseRef: "main"}
	for _, test := range []struct {
		body io.ReadCloser
		want string
	}{
		{body: failingBody{readErr: errors.New("read secret-token")}, want: "could not be read"},
		{body: failingBody{data: "[]", closeErr: errors.New("close")}, want: "could not be closed"},
	} {
		t.Run(test.want, func(t *testing.T) {
			c := NewGitHub(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: test.body, Header: make(http.Header)}, nil
			})})
			_, err := c.FindMerged(context.Background(), q)
			if err == nil || !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), "secret-token") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestGitHubRejectsMalformedMergeTimestampWithoutLeakingResponse(t *testing.T) {
	body := `[{"state":"closed","number":7,"merged_at":"secret-token","merge_commit_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","head":{"sha":"oid","ref":"feature","repo":{"name":"source","owner":{"login":"fork"}}},"base":{"ref":"main","repo":{"name":"target","owner":{"login":"base"}}}}]`
	client := NewGitHub(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})})
	_, err := client.FindMerged(context.Background(), Query{HeadOwner: "fork", HeadRepo: "source", HeadRef: "feature", HeadSHA: "oid", BaseOwner: "base", BaseRepo: "target", BaseRef: "main"})
	if err == nil || !strings.Contains(err.Error(), "could not be decoded") || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("err=%v", err)
	}
}

type failingBody struct {
	data              string
	readErr, closeErr error
	read              bool
}

func (b failingBody) Read(p []byte) (int, error) {
	if b.readErr != nil {
		return 0, b.readErr
	}
	if b.read {
		return 0, io.EOF
	}
	copy(p, b.data)
	b.read = true
	return len(b.data), io.EOF
}
func (b failingBody) Close() error { return b.closeErr }
