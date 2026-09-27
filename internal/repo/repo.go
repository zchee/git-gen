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

// Package repo owns every git operation of git-gen: reading the git configuration, signing, initializing
// the repository, staging and committing, adding the remote, and keeping the github/gitignore checkout
// up to date. It is the only package that imports go-git.
//
// go-git's configuration loader is never used. Commits always carry an explicit author, committer and
// signer, so go-git has no reason to read the configuration while committing.
package repo

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"

	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
)

// ErrUnsigned is returned by Repository.Commit when the commit it wrote carries no signature.
var ErrUnsigned = errors.New("commit carries no signature")

// Options configures Open.
type Options struct {
	Dir    string
	Config Config
	Signer Signer
	// Now gives the author and committer time. Nil means time.Now.
	Now func() time.Time
	// ProbeIgnoreCase reports whether the file system of the .git directory dir ignores case. Nil means
	// the real probe.
	ProbeIgnoreCase func(dir string) (bool, error)
}

// CommitStep is one commit: its message and the paths to stage for it.
type CommitStep struct {
	Message string
	Paths   []string // slash-separated, relative to the working tree
	Force   bool     // stage even when the working tree's .gitignore ignores the path
}

// CommitResult tells what one CommitStep did.
type CommitResult struct {
	Hash    string   // empty when nothing was committed
	Staged  []string // paths handed to the index, whether or not their content changed
	Skipped []string // ignored paths that were not staged
}

// Repository is a git working tree opened or initialized by Open.
type Repository struct {
	repo   *git.Repository
	dir    string
	cfg    Config
	signer Signer
	now    func() time.Time
}

// Open opens the repository in opts.Dir, or initializes one there when opts.Dir has no .git. existed
// reports which of the two happened; an existing repository is never initialized again.
//
// A new repository starts on opts.Config.DefaultBranch ("main" when empty). When the file system ignores
// case, core.ignorecase = true is written to .git/config, as git init does; nothing is written otherwise.
func Open(opts Options) (r *Repository, existed bool, err error) { //nolint:gocritic // hugeParam: Open runs once per process; Options stays a value so callers build it inline.
	dir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return nil, false, fmt.Errorf("repository directory: %w", err)
	}
	r = &Repository{dir: dir, cfg: opts.Config, signer: opts.Signer, now: opts.Now}
	if r.now == nil {
		r.now = time.Now
	}

	gitDir := filepath.Join(dir, git.GitDirName)
	_, err = os.Lstat(gitDir)
	switch {
	case err == nil:
		r.repo, err = git.PlainOpen(dir)
		if err != nil {
			return nil, false, fmt.Errorf("open repository %s: %w", dir, err)
		}
		return r, true, nil
	case !errors.Is(err, fs.ErrNotExist):
		return nil, false, fmt.Errorf("look for %s: %w", gitDir, err)
	}

	branch := plumbing.NewBranchReferenceName(cmp.Or(opts.Config.DefaultBranch, defaultBranch))
	r.repo, err = git.PlainInit(dir, false, git.WithDefaultBranch(branch))
	if err != nil {
		return nil, false, fmt.Errorf("initialize repository %s: %w", dir, err)
	}

	probe := opts.ProbeIgnoreCase
	if probe == nil {
		probe = probeIgnoreCase
	}
	ignoreCase, err := probe(gitDir)
	if err != nil {
		return nil, false, fmt.Errorf("probe whether %s ignores case: %w", gitDir, err)
	}
	if ignoreCase {
		cfg, err := r.repo.Config()
		if err != nil {
			return nil, false, fmt.Errorf("read %s/config: %w", gitDir, err)
		}
		cfg.Raw.Section("core").SetOption("ignorecase", "true")
		if err := r.repo.SetConfig(cfg); err != nil {
			return nil, false, fmt.Errorf("write core.ignorecase: %w", err)
		}
	}
	return r, false, nil
}

// probeIgnoreCase does what git init does: it creates a file with a mixed-case name in the .git
// directory and checks whether the same name in the opposite case resolves to it.
func probeIgnoreCase(gitDir string) (ignoreCase bool, err error) {
	f, err := os.CreateTemp(gitDir, "CaseProbe")
	if err != nil {
		return false, fmt.Errorf("create probe file: %w", err)
	}
	name := f.Name()
	defer func() {
		if rmErr := os.Remove(name); rmErr != nil && err == nil {
			err = fmt.Errorf("remove probe file: %w", rmErr)
		}
	}()
	if err := f.Close(); err != nil {
		return false, fmt.Errorf("close probe file: %w", err)
	}

	orig, err := os.Stat(name)
	if err != nil {
		return false, fmt.Errorf("stat probe file: %w", err)
	}
	other, err := os.Stat(filepath.Join(gitDir, swapCase(filepath.Base(name))))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat probe file in other case: %w", err)
	}
	return os.SameFile(orig, other), nil
}

func swapCase(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsUpper(r) {
			return unicode.ToLower(r)
		}
		return unicode.ToUpper(r)
	}, s)
}

// AddRemote adds the remote name with url unless a remote of that name exists. When it exists, nothing
// changes, effectiveURL is its first URL and added is false.
func (r *Repository) AddRemote(name, url string) (effectiveURL string, added bool, err error) {
	remote, err := r.repo.Remote(name)
	switch {
	case err == nil:
		if urls := remote.Config().URLs; len(urls) > 0 {
			return urls[0], false, nil
		}
		return "", false, nil
	case !errors.Is(err, git.ErrRemoteNotFound):
		return "", false, fmt.Errorf("read remote %s: %w", name, err)
	}

	if _, err := r.repo.CreateRemote(&config.RemoteConfig{Name: name, URLs: []string{url}}); err != nil {
		return "", false, fmt.Errorf("add remote %s: %w", name, err)
	}
	return url, true, nil
}

// Commit stages the paths of step and commits them, signed, with step.Message.
//
// A path that does not exist is left out. Unless step.Force is set, a path that the working tree's
// .gitignore files ignore is put in Skipped instead of being staged. A commit is made only when the index
// holds at least one added file, as `git status --porcelain | grep '^A'` would show; otherwise Hash is
// empty and the error is nil. Files staged earlier by someone else go into the same commit.
//
// The message ends with one newline, as with git commit -m. The signing program runs with ctx. The commit
// is read back, and ErrUnsigned is returned when its signature is empty.
func (r *Repository) Commit(ctx context.Context, step CommitStep) (CommitResult, error) {
	if r.signer == nil {
		return CommitResult{}, errors.New("commit: no signer configured")
	}
	if r.cfg.Author.Name == "" || r.cfg.Author.Email == "" || r.cfg.Committer.Name == "" || r.cfg.Committer.Email == "" {
		return CommitResult{}, errors.New("commit: author or committer identity is incomplete")
	}
	// git commit -m ends the message with exactly one newline; go-git stores it as given.
	message := strings.TrimRight(step.Message, " \t\r\n") + "\n"
	if strings.TrimSpace(message) == "" {
		return CommitResult{}, errors.New("commit: empty message")
	}
	wt, err := r.repo.Worktree()
	if err != nil {
		return CommitResult{}, fmt.Errorf("open worktree: %w", err)
	}

	var res CommitResult
	ignore := newIgnoreMatcher(r.dir)
	for _, p := range step.Paths {
		if !filepath.IsLocal(filepath.FromSlash(p)) || path.Clean(p) != p {
			return res, fmt.Errorf("stage %q: not a clean relative path inside the working tree", p)
		}
		fi, err := os.Lstat(filepath.Join(r.dir, filepath.FromSlash(p)))
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			continue
		}
		if err != nil {
			return res, fmt.Errorf("stage %s: %w", p, err)
		}
		if !step.Force {
			ignored, err := ignore.ignored(p, fi.IsDir())
			if err != nil {
				return res, fmt.Errorf("stage %s: %w", p, err)
			}
			if ignored {
				res.Skipped = append(res.Skipped, p)
				continue
			}
		}
		if _, err := wt.Add(p); err != nil {
			return res, fmt.Errorf("stage %s: %w", p, err)
		}
		res.Staged = append(res.Staged, p)
	}

	added, err := hasAddedFile(wt)
	if err != nil || !added {
		return res, err
	}

	when := r.now()
	hash, err := wt.Commit(message, &git.CommitOptions{
		Author:    &object.Signature{Name: r.cfg.Author.Name, Email: r.cfg.Author.Email, When: when},
		Committer: &object.Signature{Name: r.cfg.Committer.Name, Email: r.cfg.Committer.Email, When: when},
		// go-git v6.0.0-alpha.5 calls Sign with context.TODO(); the caller's ctx is bound here instead.
		Signer: signerFunc(func(_ context.Context, message io.Reader) ([]byte, error) {
			return sign(ctx, r.signer, message)
		}),
	})
	if err != nil {
		return res, fmt.Errorf("commit %q: %w", step.Message, err)
	}
	res.Hash = hash.String()

	c, err := r.repo.CommitObject(hash)
	if err != nil {
		return res, fmt.Errorf("read back commit %s: %w", hash, err)
	}
	if strings.TrimSpace(c.Signature) == "" {
		return res, fmt.Errorf("commit %s %q: %w", hash, step.Message, ErrUnsigned)
	}
	return res, nil
}

func hasAddedFile(wt *git.Worktree) (bool, error) {
	status, err := wt.Status()
	if err != nil {
		return false, fmt.Errorf("worktree status: %w", err)
	}
	for _, s := range status {
		if s.Staging == git.Added {
			return true, nil
		}
	}
	return false, nil
}
