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
	"errors"
	"os"
	"path/filepath"
	"testing"

	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	gocmp "github.com/google/go-cmp/cmp"
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

// commitOn commits one file on branch, which is created from HEAD when it does not exist yet, and checks
// main out again, so that a clone still starts on main. It returns the new tip of branch.
func (u *upstream) commitOn(t *testing.T, branch, name, content string) plumbing.Hash {
	t.Helper()
	wt, err := u.repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	ref := plumbing.NewBranchReferenceName(branch)
	_, err = u.repo.Reference(ref, false)
	create := errors.Is(err, plumbing.ErrReferenceNotFound)
	if err := wt.Checkout(&git.CheckoutOptions{Branch: ref, Create: create}); err != nil {
		t.Fatal(err)
	}
	h := u.commit(t, name, content)
	if err := wt.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName("main")}); err != nil {
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

// remoteRefsOf returns the remote-tracking branches of the repository in dir and the commits they point to.
// Symbolic references such as refs/remotes/origin/HEAD are left out.
func remoteRefsOf(t *testing.T, dir string) map[plumbing.ReferenceName]plumbing.Hash {
	t.Helper()
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	refs, err := repo.References()
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[plumbing.ReferenceName]plumbing.Hash)
	err = refs.ForEach(func(ref *plumbing.Reference) error {
		if ref.Name().IsRemote() && ref.Type() == plumbing.HashReference {
			got[ref.Name()] = ref.Hash()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestSync(t *testing.T) {
	t.Parallel()

	type env struct {
		up  *upstream
		dir string // the checkout
		url string
		// wantRemotes, when setup sets it, is every remote-tracking branch the checkout must have afterwards.
		wantRemotes map[plumbing.ReferenceName]plumbing.Hash
	}
	tests := map[string]struct {
		// setup prepares the checkout and returns the HEAD it must have afterwards, or the zero hash when
		// the checkout must not be a repository.
		setup      func(t *testing.T, e *env) plumbing.Hash
		pull       bool
		cancel     bool
		wantAction SyncAction
		wantErr    bool
		wantGone   bool   // the checkout directory must not exist afterwards
		wantStatus string // git status --porcelain=v1 of the checkout afterwards
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
		"success: only main is fetched": {
			setup: func(t *testing.T, e *env) plumbing.Hash {
				t.Helper()
				mustSync(t, e.dir, e.url)
				e.up.commitOn(t, "side", "Side.gitignore", "side/\n")
				head := e.up.commit(t, "Rust.gitignore", "target/\n")
				e.wantRemotes = map[plumbing.ReferenceName]plumbing.Hash{
					plumbing.NewRemoteReferenceName("origin", "main"): head,
				}
				return head
			},
			pull:       true,
			wantAction: SyncPulled,
		},
		"success: a moved side branch leaves main up to date": {
			setup: func(t *testing.T, e *env) plumbing.Hash {
				t.Helper()
				side := e.up.commitOn(t, "side", "Side.gitignore", "side/\n")
				mustSync(t, e.dir, e.url)
				e.up.commitOn(t, "side", "Side.gitignore", "side/\nmore/\n")
				head := headOf(t, e.up.dir)
				e.wantRemotes = map[plumbing.ReferenceName]plumbing.Hash{
					plumbing.NewRemoteReferenceName("origin", "main"): head,
					plumbing.NewRemoteReferenceName("origin", "side"): side,
				}
				return head
			},
			pull:       true,
			wantAction: SyncUpToDate,
		},
		"success: local commits on top of main are up to date": {
			setup: func(t *testing.T, e *env) plumbing.Hash {
				t.Helper()
				mustSync(t, e.dir, e.url)
				return localCommit(t, e.dir, "Local.gitignore", "local/\n")
			},
			pull:       true,
			wantAction: SyncUpToDate,
		},
		"success: a repository without commits gets main": {
			setup: func(t *testing.T, e *env) plumbing.Hash {
				t.Helper()
				r, err := git.PlainInit(e.dir, false, git.WithDefaultBranch(plumbing.NewBranchReferenceName("main")))
				if err != nil {
					t.Fatal(err)
				}
				defer r.Close()
				if _, err := r.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{e.url}}); err != nil {
					t.Fatal(err)
				}
				return headOf(t, e.up.dir)
			},
			pull:       true,
			wantAction: SyncPulled,
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
		"error: a checkout that diverged from main is not merged": {
			setup: func(t *testing.T, e *env) plumbing.Hash {
				t.Helper()
				mustSync(t, e.dir, e.url)
				e.up.commit(t, "Rust.gitignore", "target/\n")
				return localCommit(t, e.dir, "Local.gitignore", "local/\n")
			},
			pull:       true,
			wantAction: SyncPulled,
			wantErr:    true,
		},
		"error: uncommitted changes stop the fast-forward before HEAD moves": {
			setup: func(t *testing.T, e *env) plumbing.Hash {
				t.Helper()
				mustSync(t, e.dir, e.url)
				writeFiles(t, e.dir, map[string]string{"Go.gitignore": "*.test\n*.out\n"})
				e.up.commit(t, "Rust.gitignore", "target/\n")
				return headOf(t, e.dir)
			},
			pull:       true,
			wantAction: SyncPulled,
			wantErr:    true,
			wantStatus: " M Go.gitignore\n",
		},
		"error: a path the system rejects cannot be looked up": {
			setup: func(t *testing.T, e *env) plumbing.Hash {
				t.Helper()
				e.dir += "\x00"
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
			if e.wantRemotes != nil {
				if diff := gocmp.Diff(e.wantRemotes, remoteRefsOf(t, e.dir)); diff != "" {
					t.Errorf("remote-tracking branches of the checkout mismatch (-want +got):\n%s", diff)
				}
			}
			gitCLI(t, e.dir, "fsck", "--strict")
			if out := gitCLI(t, e.dir, "status", "--porcelain=v1"); out != tt.wantStatus {
				t.Errorf("git status of the checkout = %q, want %q", out, tt.wantStatus)
			}
		})
	}
}

// localCommit commits one file in the checkout in dir, unsigned, and returns the new HEAD.
func localCommit(t *testing.T, dir, name, content string) plumbing.Hash {
	t.Helper()
	r, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	return (&upstream{dir: dir, repo: r}).commit(t, name, content)
}

// mustSync clones url into dir and fails the test on any other outcome.
func mustSync(t *testing.T, dir, url string) {
	t.Helper()
	action, err := Sync(t.Context(), dir, url, true)
	if err != nil || action != SyncCloned {
		t.Fatalf("initial Sync() = %d, %v, want a clone", action, err)
	}
}
