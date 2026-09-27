// Copyright 2026 The git-gen Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package github applies git-gen's repository settings to the GitHub repository that origin points to.
package github

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/auth"
	"github.com/cli/go-gh/v2/pkg/repository"
	"github.com/go-json-experiment/json"
)

const (
	githubHost     = "github.com"
	githubAPIHost  = "api.github.com"
	defaultTimeout = 10 * time.Second
	maxRedirects   = 10
)

// Outcome reports what Apply did.
type Outcome int

const (
	// Failed is the zero value. Apply returns it together with every non-nil error, so a caller that ignores
	// the error still does not read a failure as success.
	Failed Outcome = iota
	// Applied means GitHub accepted the settings.
	Applied
	// SkippedNoToken means no token was found for github.com. No request was sent.
	SkippedNoToken
	// SkippedNotGitHub means the origin URL does not parse or its host is not github.com. No request was sent.
	SkippedNotGitHub
	// SkippedOriginMismatch means origin points to a repository other than <org>/<project>. No request was sent.
	SkippedOriginMismatch
	// SkippedNotFound means GitHub answered 404 to the GET: the repository does not exist or the token cannot see it.
	SkippedNotFound
)

// settings is the body of the PATCH. Three values are false and must be sent, so no field carries omitzero.
//
//nolint:tagliatelle // GitHub REST field names are snake_case.
type settings struct {
	AllowUpdateBranch   bool `json:"allow_update_branch"`
	DeleteBranchOnMerge bool `json:"delete_branch_on_merge"`
	AllowAutoMerge      bool `json:"allow_auto_merge"`
	AllowMergeCommit    bool `json:"allow_merge_commit"`
	AllowRebaseMerge    bool `json:"allow_rebase_merge"`
	HasWiki             bool `json:"has_wiki"`
}

// repoSettings mirrors the flags the script passed to "gh repo edit".
var repoSettings = settings{
	AllowUpdateBranch:   true,
	DeleteBranchOnMerge: true,
	AllowAutoMerge:      true,
	AllowMergeCommit:    false,
	AllowRebaseMerge:    false,
	HasWiki:             false,
}

// Options configures a Client.
type Options struct {
	// Token returns the token for a host. Nil means go-gh's auth.TokenForHost, which reads GH_TOKEN and
	// GITHUB_TOKEN, then the gh configuration, and finally runs "gh auth token".
	Token func(host string) string

	// API configures the REST client. New fills the zero fields that would otherwise make go-gh read the
	// gh configuration: Host becomes github.com, APIHost api.github.com, and Transport http.DefaultTransport
	// unless UnixDomainSocket is set. Timeout defaults to 10 seconds. GH_DEBUG is always ignored, so
	// logging happens only when Log is set. AuthToken is replaced by the value of Token on every call.
	API api.ClientOptions
}

// Client changes the settings of a GitHub repository.
type Client struct {
	token func(host string) string
	api   api.ClientOptions
}

// New returns a Client configured by opts.
func New(opts Options) *Client { //nolint:gocritic // hugeParam: New runs once per process and its signature is fixed by the package contract.
	c := &Client{token: opts.Token, api: opts.API}
	if c.token == nil {
		c.token = func(host string) string {
			token, _ := auth.TokenForHost(host)
			return token
		}
	}
	if c.api.Host == "" {
		c.api.Host = githubHost
	}
	if c.api.APIHost == "" {
		c.api.APIHost = githubAPIHost
	}
	if c.api.Transport == nil && c.api.UnixDomainSocket == "" {
		c.api.Transport = http.DefaultTransport
	}
	if c.api.Timeout == 0 {
		c.api.Timeout = defaultTimeout
	}
	if c.api.CheckRedirect == nil {
		c.api.CheckRedirect = keepMethod
	}
	c.api.LogIgnoreEnv = true
	return c
}

// Apply sends the settings to the repository that originURL points to when it is github.com/<org>/<project>.
//
// No request is sent when originURL is not a github.com URL, names another repository, or no token is found.
// A 404 from the GET is SkippedNotFound. Any other failure of the GET or the PATCH is returned as an error
// together with Failed.
func (c *Client) Apply(ctx context.Context, originURL, org, project string) (Outcome, error) {
	repo, err := repository.ParseWithHost(originURL, "")
	if err != nil || auth.NormalizeHostname(repo.Host) != githubHost {
		return SkippedNotGitHub, nil //nolint:nilerr // An origin that does not parse is an outcome, not a failure.
	}
	name := strings.TrimSuffix(repo.Name, ".git")
	if !strings.EqualFold(repo.Owner, org) || !strings.EqualFold(name, project) {
		return SkippedOriginMismatch, nil
	}

	token := c.token(c.api.Host)
	if token == "" {
		return SkippedNoToken, nil
	}
	opts := c.api
	opts.AuthToken = token
	client, err := api.NewRESTClient(opts)
	if err != nil {
		return Failed, fmt.Errorf("github: create REST client for %s/%s: %w", repo.Owner, name, err)
	}

	path := "repos/" + url.PathEscape(repo.Owner) + "/" + url.PathEscape(name)
	if err := send(ctx, client, http.MethodGet, path, nil); err != nil {
		if httpErr, ok := errors.AsType[*api.HTTPError](err); ok && httpErr.StatusCode == http.StatusNotFound {
			return SkippedNotFound, nil
		}
		return Failed, fmt.Errorf("github: get repository %s/%s: %w", repo.Owner, name, err)
	}

	body, err := json.Marshal(repoSettings)
	if err != nil {
		return Failed, fmt.Errorf("github: encode settings for %s/%s: %w", repo.Owner, name, err)
	}
	if err := send(ctx, client, http.MethodPatch, path, body); err != nil {
		return Failed, fmt.Errorf("github: update settings of %s/%s: %w", repo.Owner, name, err)
	}
	return Applied, nil
}

// send issues one request. A non-2xx status is an *api.HTTPError. The body is not read: the status decides the result.
func send(ctx context.Context, client *api.RESTClient, method, path string, body []byte) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	resp, err := client.RequestWithContext(ctx, method, path, reader)
	if err != nil {
		return err //nolint:wrapcheck // The caller wraps it with the operation and the repository.
	}
	defer resp.Body.Close()
	return nil
}

// keepMethod refuses a redirect that changes the method. net/http turns a PATCH answered with 301, 302 or 303
// into a GET, which would report success without changing anything. The redirect response is returned as is,
// so go-gh reports its status as an *api.HTTPError.
func keepMethod(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	if req.Method != via[0].Method {
		return http.ErrUseLastResponse
	}
	return nil
}
