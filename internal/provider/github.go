package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const githubAPI = "https://api.github.com"
const maxResponseBytes = 2 << 20

// HTTPDoer permits deterministic tests without coupling this package to app or Git.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// GitHub queries GitHub's documented closed pull-request endpoint.
type GitHub struct {
	http    HTTPDoer
	timeout time.Duration
}

func NewGitHub(client HTTPDoer) *GitHub {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("GitHub redirects are refused") }}
	}
	return &GitHub{http: client, timeout: 10 * time.Second}
}

func (g *GitHub) FindMerged(ctx context.Context, q Query) (PullRequest, error) {
	if err := validQuery(q); err != nil {
		return PullRequest{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()
	for page := 1; page <= 10; page++ {
		pulls, err := g.page(ctx, q, page)
		if err != nil {
			return PullRequest{}, err
		}
		if proof, found, err := exactProof(pulls, q); err != nil || found {
			return proof, err
		}
		if len(pulls) < 100 {
			return PullRequest{}, errors.New("no exact merged pull request proof found")
		}
	}
	return PullRequest{}, errors.New("GitHub pull request pagination limit reached without exact proof")
}

func (g *GitHub) page(ctx context.Context, q Query, page int) ([]githubPull, error) {
	v := url.Values{"state": {"closed"}, "head": {q.HeadOwner + ":" + q.HeadRef}, "base": {q.BaseRef}, "per_page": {"100"}, "page": {fmt.Sprint(page)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubAPI+"/repos/"+url.PathEscape(q.BaseOwner)+"/"+url.PathEscape(q.BaseRepo)+"/pulls?"+v.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token := os.Getenv("GH_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	} else if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := g.http.Do(req)
	if err != nil {
		return nil, errors.New("GitHub pull request query failed")
	}
	return decodePulls(resp)
}

func decodePulls(resp *http.Response) ([]githubPull, error) {
	if resp.StatusCode != http.StatusOK {
		_, drainErr := io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		closeErr := resp.Body.Close()
		if drainErr != nil || closeErr != nil {
			return nil, errors.New("GitHub pull request response could not be read")
		}
		return nil, fmt.Errorf("GitHub pull request query returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	closeErr := resp.Body.Close()
	if err != nil {
		return nil, errors.New("GitHub pull request response could not be read")
	}
	if closeErr != nil {
		return nil, errors.New("GitHub pull request response could not be closed")
	}
	if len(data) > maxResponseBytes {
		return nil, errors.New("GitHub response exceeds size limit")
	}
	var pulls []githubPull
	if json.Unmarshal(data, &pulls) != nil {
		return nil, errors.New("GitHub pull request response could not be decoded")
	}
	return pulls, nil
}

func exactProof(pulls []githubPull, q Query) (PullRequest, bool, error) {
	for _, p := range pulls {
		if !matches(p, q) {
			continue
		}
		if strings.TrimSpace(p.MergeCommitSHA) == "" {
			return PullRequest{}, false, errors.New("merged pull request has no merge commit SHA")
		}
		return PullRequest{Number: p.Number, URL: p.HTMLURL, MergedAt: p.MergedAt.UTC(), HeadSHA: p.Head.SHA, HeadOwner: p.Head.Repo.Owner.Login, HeadRepo: p.Head.Repo.Name, HeadRef: p.Head.Ref, BaseOwner: p.Base.Repo.Owner.Login, BaseRepo: p.Base.Repo.Name, BaseRef: p.Base.Ref, MergeCommitSHA: p.MergeCommitSHA}, true, nil
	}
	return PullRequest{}, false, nil
}

func matches(p githubPull, q Query) bool {
	return p.State == "closed" && p.MergedAt != nil && !p.MergedAt.IsZero() && p.Head.SHA == q.HeadSHA && p.Head.Ref == q.HeadRef && p.Head.Repo.Owner.Login == q.HeadOwner && p.Head.Repo.Name == q.HeadRepo && p.Base.Ref == q.BaseRef && p.Base.Repo.Owner.Login == q.BaseOwner && p.Base.Repo.Name == q.BaseRepo
}

func validQuery(q Query) error {
	for _, v := range []string{q.HeadOwner, q.HeadRepo, q.HeadRef, q.BaseOwner, q.BaseRepo, q.BaseRef, q.HeadSHA} {
		if strings.TrimSpace(v) == "" {
			return errors.New("GitHub query requires complete immutable repository, ref, and SHA identity")
		}
	}
	return nil
}

type githubPull struct {
	State          string     `json:"state"`
	Number         int        `json:"number"`
	HTMLURL        string     `json:"html_url"`
	MergedAt       *time.Time `json:"merged_at"`
	MergeCommitSHA string     `json:"merge_commit_sha"`
	Head           githubSide `json:"head"`
	Base           githubSide `json:"base"`
}
type githubSide struct {
	SHA  string     `json:"sha"`
	Ref  string     `json:"ref"`
	Repo githubRepo `json:"repo"`
}
type githubRepo struct {
	Name  string `json:"name"`
	Owner struct {
		Login string `json:"login"`
	} `json:"owner"`
}
