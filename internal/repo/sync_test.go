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

package repo

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
)

// upstream is a local repository that stands for github/gitignore. The go-git file transport serves it
// in process; no git binary and no network are involved.
type upstream struct {
	dir  string
	repo *git.Repository
}

func newUpstream(t *testing.T) *upstream {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false, git.WithDefaultBranch(plumbing.NewBranchReferenceName("main")))
	if err != nil {
		t.Fatal(err)
	}
	u := &upstream{dir: dir, repo: repo}
	u.commit(t, "Go.gitignore", "*.test\n")
	return u
}

// commit adds one file and commits it, unsigned, returning the new HEAD.
func (u *upstream) commit(t *testing.T, name, content string) plumbing.Hash {
	t.Helper()
	writeFiles(t, u.dir, map[string]string{name: content})
	wt, err := u.repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add(name); err != nil {
		t.Fatal(err)
	}
	who := &object.Signature{Name: "Upstream", Email: "upstream@example.com", When: fixedNow}
	h, err := wt.Commit("add "+name+"\n", &git.CommitOptions{Author: who, Committer: who})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// headOf returns the commit HEAD of the repository in dir points to.
func headOf(t *testing.T, dir string) plumbing.Hash {
	t.Helper()
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	return head.Hash()
}

func TestSync(t *testing.T) {
	t.Parallel()

	type env struct {
		up  *upstream
		dir string // the checkout
		url string
	}
	tests := map[string]struct {
		// setup prepares the checkout and returns the HEAD it must have afterwards, or the zero hash when
		// the checkout must not be a repository.
		setup      func(t *testing.T, e *env) plumbing.Hash
		pull       bool
		cancel     bool
		wantAction SyncAction
		wantErr    bool
		wantGone   bool // the checkout directory must not exist afterwards
	}{
		"success: a missing checkout is cloned": {
			setup:      func(t *testing.T, e *env) plumbing.Hash { t.Helper(); return headOf(t, e.up.dir) },
			pull:       true,
			wantAction: SyncCloned,
		},
		"success: a missing checkout is cloned even without pull": {
			setup:      func(t *testing.T, e *env) plumbing.Hash { t.Helper(); return headOf(t, e.up.dir) },
			pull:       false,
			wantAction: SyncCloned,
		},
		"success: missing parent directories are created": {
			setup: func(t *testing.T, e *env) plumbing.Hash {
				t.Helper()
				e.dir = filepath.Join(e.dir, "src", "github.com", "github", "gitignore")
				return headOf(t, e.up.dir)
			},
			pull:       true,
			wantAction: SyncCloned,
		},
		"success: new upstream commits are pulled": {
			setup: func(t *testing.T, e *env) plumbing.Hash {
				t.Helper()
				mustSync(t, e.dir, e.url)
				return e.up.commit(t, "Rust.gitignore", "target/\n")
			},
			pull:       true,
			wantAction: SyncPulled,
		},
		"success: an up-to-date checkout reports it": {
			setup: func(t *testing.T, e *env) plumbing.Hash {
				t.Helper()
				mustSync(t, e.dir, e.url)
				return headOf(t, e.up.dir)
			},
			pull:       true,
			wantAction: SyncUpToDate,
		},
		"success: without pull an existing checkout is left alone": {
			setup: func(t *testing.T, e *env) plumbing.Hash {
				t.Helper()
				mustSync(t, e.dir, e.url)
				before := headOf(t, e.dir)
				e.up.commit(t, "Rust.gitignore", "target/\n")
				return before
			},
			pull:       false,
			wantAction: SyncSkippedNoPull,
		},
		"success: a directory that is not a repository is skipped": {
			setup: func(t *testing.T, e *env) plumbing.Hash {
				t.Helper()
				writeFiles(t, e.dir, map[string]string{"Go.gitignore": "*.test\n"})
				return plumbing.ZeroHash
			},
			pull:       true,
			wantAction: SyncSkippedNotRepository,
		},
		"error: cloning a missing repository leaves no directory behind": {
			setup: func(t *testing.T, e *env) plumbing.Hash {
				t.Helper()
				e.url = filepath.Join(t.TempDir(), "missing")
				return plumbing.ZeroHash
			},
			pull:       true,
			wantAction: SyncCloned,
			wantErr:    true,
			wantGone:   true,
		},
		"error: a cancelled context stops the clone": {
			setup:      func(*testing.T, *env) plumbing.Hash { return plumbing.ZeroHash },
			pull:       true,
			cancel:     true,
			wantAction: SyncCloned,
			wantErr:    true,
			wantGone:   true,
		},
		"error: a checkout without an origin cannot be pulled": {
			setup: func(t *testing.T, e *env) plumbing.Hash {
				t.Helper()
				if _, err := git.PlainInit(e.dir, false); err != nil {
					t.Fatal(err)
				}
				return plumbing.ZeroHash
			},
			pull:       true,
			wantAction: SyncPulled,
			wantErr:    true,
		},
		"error: a bare repository has no worktree to pull into": {
			setup: func(t *testing.T, e *env) plumbing.Hash {
				t.Helper()
				if _, err := git.PlainInit(e.dir, true); err != nil {
					t.Fatal(err)
				}
				return plumbing.ZeroHash
			},
			pull:       true,
			wantAction: SyncPulled,
			wantErr:    true,
		},
		"error: the path is a file": {
			setup: func(t *testing.T, e *env) plumbing.Hash {
				t.Helper()
				e.dir = filepath.Join(e.dir, "file")
				writeFiles(t, filepath.Dir(e.dir), map[string]string{"file": ""})
				return plumbing.ZeroHash
			},
			pull:       true,
			wantAction: SyncPulled,
			wantErr:    true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			up := newUpstream(t)
			e := &env{up: up, dir: filepath.Join(t.TempDir(), "gitignore"), url: up.dir}
			wantHead := tt.setup(t, e)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.cancel {
				cancel()
			}
			action, err := Sync(ctx, e.dir, e.url, tt.pull)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Sync() error = %v, want an error: %t", err, tt.wantErr)
			}
			if action != tt.wantAction {
				t.Errorf("Sync() action = %d, want %d", action, tt.wantAction)
			}
			if tt.wantGone {
				if _, err := os.Stat(e.dir); !os.IsNotExist(err) {
					t.Errorf("checkout %s exists after a failed clone (stat error %v)", e.dir, err)
				}
				return
			}
			if wantHead.IsZero() {
				return
			}
			if got := headOf(t, e.dir); got != wantHead {
				t.Errorf("checkout HEAD = %s, want %s", got, wantHead)
			}
			gitCLI(t, e.dir, "fsck", "--strict")
			if out := gitCLI(t, e.dir, "status", "--porcelain=v1"); out != "" {
				t.Errorf("git status of the checkout is not clean:\n%s", out)
			}
		})
	}
}

// mustSync clones url into dir and fails the test on any other outcome.
func mustSync(t *testing.T, dir, url string) {
	t.Helper()
	action, err := Sync(t.Context(), dir, url, true)
	if err != nil || action != SyncCloned {
		t.Fatalf("initial Sync() = %d, %v, want a clone", action, err)
	}
}
