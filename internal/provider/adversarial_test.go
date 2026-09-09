package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGitHubCancellationThenFreshRetryWithSameClient(t *testing.T) {
	t.Setenv("GH_TOKEN", "ultraqa-fake-token")
	t.Setenv("GITHUB_TOKEN", "")
	started := make(chan struct{}, 1)
	handlerDone := make(chan struct{}, 1)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer ultraqa-fake-token" {
			t.Errorf("Authorization = %q", got)
		}
		if calls.Add(1) == 1 {
			started <- struct{}{}
			<-r.Context().Done()
			handlerDone <- struct{}{}
			return
		}
		_, _ = io.WriteString(w, `[{"state":"closed","number":7,"html_url":"https://github.com/base/target/pull/7","merged_at":"2026-01-02T03:04:05Z","merge_commit_sha":"merge","head":{"sha":"head","ref":"feature","repo":{"name":"source","owner":{"login":"fork"}}},"base":{"ref":"main","repo":{"name":"target","owner":{"login":"base"}}}}]`)
	}))
	t.Cleanup(server.Close)
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := NewGitHub(&http.Client{Transport: rewriteGitHubTo(endpoint)})
	query := Query{HeadOwner: "fork", HeadRepo: "source", HeadRef: "feature", HeadSHA: "head", BaseOwner: "base", BaseRepo: "target", BaseRef: "main"}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := client.FindMerged(ctx, query)
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("provider request did not reach test server")
	}
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "GitHub pull request query failed") {
			t.Fatalf("cancelled FindMerged error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled FindMerged did not return")
	}
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("cancelled server handler did not finish")
	}

	proof, err := client.FindMerged(context.Background(), query)
	if err != nil {
		t.Fatalf("fresh retry failed: %v", err)
	}
	if proof.Number != 7 || proof.HeadSHA != query.HeadSHA || proof.BaseRepo != query.BaseRepo || calls.Load() != 2 {
		t.Fatalf("fresh proof = %+v, calls = %d", proof, calls.Load())
	}
}

func rewriteGitHubTo(endpoint *url.URL) http.RoundTripper {
	return roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		copy := request.Clone(request.Context())
		copy.URL = new(url.URL)
		*copy.URL = *request.URL
		copy.URL.Scheme = endpoint.Scheme
		copy.URL.Host = endpoint.Host
		copy.Host = ""
		return http.DefaultTransport.RoundTrip(copy)
	})
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
