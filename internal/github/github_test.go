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

package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/go-json-experiment/json"
	gocmp "github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// testToken is a fake token. Tests assert that it never appears in an error.
const testToken = "test-token-5f0c9a7e21"

// wantSettings is the PATCH body of plan 6.8, field for field.
var wantSettings = map[string]any{
	"allow_update_branch":    true,
	"delete_branch_on_merge": true,
	"allow_auto_merge":       true,
	"allow_merge_commit":     false,
	"allow_rebase_merge":     false,
	"has_wiki":               false,
}

// reply is how the test server answers one method.
type reply struct {
	status   int
	location string
	block    bool // wait until the client goes away
}

// received is one request as the test server saw it.
type received struct {
	method        string
	path          string
	authorization string
	body          []byte
}

// server is an httptest server standing in for api.github.com. It records every request it receives.
type server struct {
	srv   *httptest.Server
	get   reply
	patch reply

	mu   sync.Mutex
	reqs []received
}

func newServer(t *testing.T, get, patch reply) *server {
	t.Helper()
	s := &server{get: get, patch: patch}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serveHTTP))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.reqs = append(s.reqs, received{method: r.Method, path: r.URL.Path, authorization: r.Header.Get("Authorization"), body: body})
	s.mu.Unlock()

	rep := s.get
	if r.Method == http.MethodPatch {
		rep = s.patch
	}
	if rep.block {
		<-r.Context().Done()
		return
	}
	if rep.location != "" {
		w.Header().Set("Location", rep.location)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(rep.status)
	if rep.status >= http.StatusMultipleChoices {
		fmt.Fprintf(w, `{"message":%q}`, http.StatusText(rep.status))
		return
	}
	fmt.Fprint(w, `{}`)
}

// requests returns what the server received so far.
func (s *server) requests() []received {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.reqs)
}

// transport sends the client's requests to the test server, keeping the path and the headers go-gh set.
func (s *server) transport(t *testing.T) http.RoundTripper {
	t.Helper()
	target, err := url.Parse(s.srv.URL)
	if err != nil {
		t.Fatalf("parse test server URL %q: %v", s.srv.URL, err)
	}
	return rewriteHost{target: target, next: s.srv.Client().Transport}
}

type rewriteHost struct {
	target *url.URL
	next   http.RoundTripper
}

func (rt rewriteHost) RoundTrip(req *http.Request) (*http.Response, error) {
	out := req.Clone(req.Context())
	out.URL.Scheme = rt.target.Scheme
	out.URL.Host = rt.target.Host
	return rt.next.RoundTrip(out)
}

// TestOutcome pins the constant order of contract section 9. Failed must be the zero value, so that a caller that
// ignores the error never reads a failure as success.
func TestOutcome(t *testing.T) {
	t.Parallel()

	var zero Outcome
	tests := map[string]struct {
		got  Outcome
		want Outcome
	}{
		"success: the zero value of Outcome is Failed": {got: zero, want: Failed},
		"success: Failed is 0":                         {got: Failed, want: 0},
		"success: Applied is 1":                        {got: Applied, want: 1},
		"success: SkippedNoToken is 2":                 {got: SkippedNoToken, want: 2},
		"success: SkippedNotGitHub is 3":               {got: SkippedNotGitHub, want: 3},
		"success: SkippedOriginMismatch is 4":          {got: SkippedOriginMismatch, want: 4},
		"success: SkippedNotFound is 5":                {got: SkippedNotFound, want: 5},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tt.got != tt.want {
				t.Errorf("Outcome = %d, want %d", tt.got, tt.want)
			}
		})
	}
}

func TestApply(t *testing.T) {
	t.Parallel()

	const (
		org     = "org"
		project = "project"
	)
	ok := reply{status: http.StatusOK}
	getReq := "GET /repos/org/project"
	patchReq := "PATCH /repos/org/project"

	tests := map[string]struct {
		origin     string
		token      string
		get, patch reply
		apiHost    string
		timeout    time.Duration
		cancel     bool

		want           Outcome
		wantTokenCalls int
		wantReqs       []string
		wantErr        bool
		wantStatus     int   // StatusCode of the *api.HTTPError in the error chain
		wantErrIs      error // a sentinel in the error chain
	}{
		"success: scp-like origin with .git": {
			origin: "git@github.com:org/project.git", token: testToken, get: ok, patch: ok,
			want: Applied, wantTokenCalls: 1, wantReqs: []string{getReq, patchReq},
		},
		"success: https origin with .git": {
			origin: "https://github.com/org/project.git", token: testToken, get: ok, patch: ok,
			want: Applied, wantTokenCalls: 1, wantReqs: []string{getReq, patchReq},
		},
		"success: https origin without .git": {
			origin: "https://github.com/org/project", token: testToken, get: ok, patch: ok,
			want: Applied, wantTokenCalls: 1, wantReqs: []string{getReq, patchReq},
		},
		"success: ssh URL origin": {
			origin: "ssh://git@github.com/org/project.git", token: testToken, get: ok, patch: ok,
			want: Applied, wantTokenCalls: 1, wantReqs: []string{getReq, patchReq},
		},
		"success: ssh over the HTTPS port normalizes to github.com": {
			origin: "ssh://git@ssh.github.com:443/org/project.git", token: testToken, get: ok, patch: ok,
			want: Applied, wantTokenCalls: 1, wantReqs: []string{getReq, patchReq},
		},
		"success: owner and name compare case-insensitively": {
			origin: "git@github.com:Org/Project.git", token: testToken, get: ok, patch: ok,
			want: Applied, wantTokenCalls: 1, wantReqs: []string{"GET /repos/Org/Project", "PATCH /repos/Org/Project"},
		},
		"success: GET 404 skips without PATCH": {
			origin: "git@github.com:org/project.git", token: testToken, get: reply{status: http.StatusNotFound},
			want: SkippedNotFound, wantTokenCalls: 1, wantReqs: []string{getReq},
		},
		"success: empty token skips without a request": {
			origin: "git@github.com:org/project.git", token: "",
			want: SkippedNoToken, wantTokenCalls: 1,
		},
		"success: origin of another owner skips without a request": {
			origin: "git@github.com:other/project.git", token: testToken,
			want: SkippedOriginMismatch,
		},
		"success: origin of another name skips without a request": {
			origin: "https://github.com/org/other.git", token: testToken,
			want: SkippedOriginMismatch,
		},
		"success: origin on another host skips without a request": {
			origin: "git@gitlab.com:org/project.git", token: testToken,
			want: SkippedNotGitHub,
		},
		"success: origin that does not parse skips without a request": {
			origin: "not a URL", token: testToken,
			want: SkippedNotGitHub,
		},
		"success: OWNER/REPO origin has no host and skips without a request": {
			origin: "org/project", token: testToken,
			want: SkippedNotGitHub,
		},
		"success: local path origin skips without a request": {
			origin: "/srv/git/org/project.git", token: testToken,
			want: SkippedNotGitHub,
		},
		"success: empty origin skips without a request": {
			origin: "", token: testToken,
			want: SkippedNotGitHub,
		},
		"error: PATCH 403": {
			origin: "git@github.com:org/project.git", token: testToken, get: ok, patch: reply{status: http.StatusForbidden},
			want: Failed, wantTokenCalls: 1, wantReqs: []string{getReq, patchReq}, wantErr: true, wantStatus: http.StatusForbidden,
		},
		"error: GET 500": {
			origin: "git@github.com:org/project.git", token: testToken, get: reply{status: http.StatusInternalServerError},
			want: Failed, wantTokenCalls: 1, wantReqs: []string{getReq}, wantErr: true, wantStatus: http.StatusInternalServerError,
		},
		"error: GET 403": {
			origin: "git@github.com:org/project.git", token: testToken, get: reply{status: http.StatusForbidden},
			want: Failed, wantTokenCalls: 1, wantReqs: []string{getReq}, wantErr: true, wantStatus: http.StatusForbidden,
		},
		"error: PATCH answered with 301 is not turned into a GET": {
			origin: "git@github.com:org/project.git", token: testToken, get: ok,
			patch: reply{status: http.StatusMovedPermanently, location: "/repos/org/renamed"},
			want:  Failed, wantTokenCalls: 1, wantReqs: []string{getReq, patchReq}, wantErr: true, wantStatus: http.StatusMovedPermanently,
		},
		"error: GET redirect loop stops after 10 requests": {
			origin: "git@github.com:org/project.git", token: testToken,
			get:  reply{status: http.StatusMovedPermanently, location: "/repos/org/project"},
			want: Failed, wantTokenCalls: 1, wantReqs: slices.Repeat([]string{getReq}, 10), wantErr: true,
		},
		"error: invalid APIHost fails before any request": {
			origin: "git@github.com:org/project.git", token: testToken, apiHost: "api.github.com:443",
			want: Failed, wantTokenCalls: 1, wantErr: true,
		},
		"error: cancelled context stops the call": {
			origin: "git@github.com:org/project.git", token: testToken, get: ok, patch: ok, cancel: true,
			want: Failed, wantTokenCalls: 1, wantErr: true, wantErrIs: context.Canceled,
		},
		"error: API.Timeout bounds a GET that never answers": {
			origin: "git@github.com:org/project.git", token: testToken, get: reply{block: true}, timeout: 250 * time.Millisecond,
			want: Failed, wantTokenCalls: 1, wantReqs: []string{getReq}, wantErr: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t, tt.get, tt.patch)
			var tokenHosts []string
			client := New(Options{
				Token: func(host string) string {
					tokenHosts = append(tokenHosts, host)
					return tt.token
				},
				API: api.ClientOptions{
					Host:      "github.com",
					APIHost:   tt.apiHost,
					Timeout:   tt.timeout,
					Transport: srv.transport(t),
				},
			})

			ctx := t.Context()
			if tt.cancel {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			got, err := client.Apply(ctx, tt.origin, org, project)

			if (err != nil) != tt.wantErr {
				t.Fatalf("Apply(%q) error = %v, wantErr %t", tt.origin, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("Apply(%q) = %d, want %d", tt.origin, got, tt.want)
			}
			if err != nil {
				msg := err.Error()
				if !strings.Contains(msg, "org/project") {
					t.Errorf("error %q does not name the repository org/project", msg)
				}
				if strings.Contains(msg, testToken) {
					t.Errorf("error %q contains the token", msg)
				}
				httpErr, isHTTP := errors.AsType[*api.HTTPError](err)
				switch {
				case tt.wantStatus != 0 && !isHTTP:
					t.Errorf("error %q: no *api.HTTPError in the chain, want status %d", msg, tt.wantStatus)
				case tt.wantStatus != 0 && httpErr.StatusCode != tt.wantStatus:
					t.Errorf("error %q: status %d, want %d", msg, httpErr.StatusCode, tt.wantStatus)
				case tt.wantStatus == 0 && isHTTP:
					t.Errorf("error %q: unexpected *api.HTTPError with status %d", msg, httpErr.StatusCode)
				}
				if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
					t.Errorf("error %q is not %v", msg, tt.wantErrIs)
				}
			}

			if diff := gocmp.Diff(slices.Repeat([]string{"github.com"}, tt.wantTokenCalls), tokenHosts, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("hosts passed to Token (-want +got):\n%s", diff)
			}

			reqs := srv.requests()
			gotReqs := make([]string, 0, len(reqs))
			for _, r := range reqs {
				gotReqs = append(gotReqs, r.method+" "+r.path)
			}
			if diff := gocmp.Diff(tt.wantReqs, gotReqs, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("requests received (-want +got):\n%s", diff)
			}
			for _, r := range reqs {
				if want := "token " + testToken; r.authorization != want {
					t.Errorf("%s %s: Authorization = %q, want %q", r.method, r.path, r.authorization, want)
				}
				if r.method != http.MethodPatch {
					if len(r.body) != 0 {
						t.Errorf("%s %s: body = %q, want none", r.method, r.path, r.body)
					}
					continue
				}
				var body map[string]any
				if err := json.Unmarshal(r.body, &body); err != nil {
					t.Fatalf("PATCH %s: decode body %q: %v", r.path, r.body, err)
				}
				if diff := gocmp.Diff(wantSettings, body); diff != "" {
					t.Errorf("PATCH %s body (-want +got):\n%s", r.path, diff)
				}
			}
		})
	}
}

// TestApplyIgnoresGhEnvironment sets every variable go-gh consults on its own to values that would change the
// result if go-gh read them. It cannot run in parallel because it sets environment variables.
func TestApplyIgnoresGhEnvironment(t *testing.T) {
	configDir := t.TempDir()
	// An api_host with a port fails go-gh's validation, so reading this file would make NewRESTClient fail.
	writeFile(t, filepath.Join(configDir, "hosts.yml"), "github.com:\n    api_host: api.github.com:1\n    oauth_token: config-token\n")
	writeFile(t, filepath.Join(configDir, "config.yml"), "http_unix_socket: "+filepath.Join(configDir, "gh.sock")+"\n")
	t.Setenv("GH_CONFIG_DIR", configDir)
	t.Setenv("GH_TOKEN", "env-token")
	t.Setenv("GITHUB_TOKEN", "env-token")
	t.Setenv("GH_HOST", "example.com")
	t.Setenv("GH_PATH", filepath.Join(configDir, "nonexistent-gh"))
	t.Setenv("GH_DEBUG", "api")

	tests := map[string]struct {
		token    string
		want     Outcome
		wantReqs int
	}{
		"success: injected token is used instead of GH_TOKEN and the gh configuration": {
			token: testToken, want: Applied, wantReqs: 2,
		},
		"success: empty injected token skips even when GH_TOKEN is set": {
			token: "", want: SkippedNoToken, wantReqs: 0,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ok := reply{status: http.StatusOK}
			srv := newServer(t, ok, ok)
			client := New(Options{
				Token: func(string) string { return tt.token },
				API:   api.ClientOptions{Host: "github.com", Transport: srv.transport(t)},
			})

			got, err := client.Apply(t.Context(), "git@github.com:org/project.git", "org", "project")
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if got != tt.want {
				t.Errorf("Apply = %d, want %d", got, tt.want)
			}
			reqs := srv.requests()
			if len(reqs) != tt.wantReqs {
				t.Fatalf("server received %d requests, want %d", len(reqs), tt.wantReqs)
			}
			for _, r := range reqs {
				if want := "token " + testToken; r.authorization != want {
					t.Errorf("%s %s: Authorization = %q, want %q", r.method, r.path, r.authorization, want)
				}
			}
		})
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	custom := &http.Transport{}

	// view is the part of the resolved options that can be compared with cmp.
	type view struct {
		Host             string
		APIHost          string
		UnixDomainSocket string
		Timeout          time.Duration
		LogIgnoreEnv     bool
	}

	tests := map[string]struct {
		opts          Options
		want          view
		wantTransport http.RoundTripper
	}{
		"success: zero options get defaults that keep go-gh off the gh configuration": {
			opts:          Options{},
			want:          view{Host: "github.com", APIHost: "api.github.com", Timeout: 10 * time.Second, LogIgnoreEnv: true},
			wantTransport: http.DefaultTransport,
		},
		"success: fields set by the caller are kept": {
			opts: Options{API: api.ClientOptions{
				Host: "github.com", APIHost: "api.example.com", Timeout: time.Second, Transport: custom,
			}},
			want:          view{Host: "github.com", APIHost: "api.example.com", Timeout: time.Second, LogIgnoreEnv: true},
			wantTransport: custom,
		},
		"success: a Unix socket leaves Transport unset so go-gh dials the socket": {
			opts:          Options{API: api.ClientOptions{UnixDomainSocket: "/run/gh.sock"}},
			want:          view{Host: "github.com", APIHost: "api.github.com", UnixDomainSocket: "/run/gh.sock", Timeout: 10 * time.Second, LogIgnoreEnv: true},
			wantTransport: nil,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := New(tt.opts)
			got := view{
				Host:             c.api.Host,
				APIHost:          c.api.APIHost,
				UnixDomainSocket: c.api.UnixDomainSocket,
				Timeout:          c.api.Timeout,
				LogIgnoreEnv:     c.api.LogIgnoreEnv,
			}
			if diff := gocmp.Diff(tt.want, got); diff != "" {
				t.Errorf("New options (-want +got):\n%s", diff)
			}
			if c.api.Transport != tt.wantTransport {
				t.Errorf("New Transport = %v, want %v", c.api.Transport, tt.wantTransport)
			}
			if c.api.CheckRedirect == nil {
				t.Error("New left CheckRedirect nil")
			}
			// The default token function is go-gh's auth.TokenForHost. It is never called here.
			if c.token == nil {
				t.Error("New left the token function nil")
			}
		})
	}
}
