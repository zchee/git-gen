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
	"fmt"
	"io/fs"
	"os"

	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
)

// syncBranch is the branch of the upstream checkout that Sync pulls.
const syncBranch = "main"

// SyncAction tells what Sync did. On error it names the operation that failed.
type SyncAction int

// The outcomes of Sync.
const (
	SyncCloned SyncAction = iota
	SyncPulled
	SyncUpToDate
	SyncSkippedNotRepository
	SyncSkippedNoPull
)

// Sync keeps the checkout in dir up to date with url, the way `git clone` and `git pull origin main` would.
//
// When dir does not exist, url is cloned into it. Otherwise, when pull is false, nothing happens. When pull
// is true, a directory that is not a git repository is left alone, and a repository fetches the main branch
// of its origin remote, and no other branch, and merges it in. Only a fast-forward is possible; local commits
// on top of main leave the repository up to date. ctx bounds the network transfer.
func Sync(ctx context.Context, dir, url string, pull bool) (SyncAction, error) {
	fi, err := os.Stat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		r, err := git.PlainCloneContext(ctx, dir, &git.CloneOptions{URL: url})
		if err != nil {
			return SyncCloned, fmt.Errorf("clone %s into %s: %w", url, dir, err)
		}
		if err := r.Close(); err != nil {
			return SyncCloned, fmt.Errorf("close %s: %w", dir, err)
		}
		return SyncCloned, nil
	case err != nil:
		return SyncPulled, fmt.Errorf("look for %s: %w", dir, err)
	case !fi.IsDir():
		return SyncPulled, fmt.Errorf("%s exists and is not a directory", dir)
	case !pull:
		return SyncSkippedNoPull, nil
	}

	r, err := git.PlainOpen(dir)
	if errors.Is(err, git.ErrRepositoryNotExists) {
		return SyncSkippedNotRepository, nil
	}
	if err != nil {
		return SyncPulled, fmt.Errorf("open %s: %w", dir, err)
	}
	defer r.Close()

	wt, err := r.Worktree()
	if err != nil {
		return SyncPulled, fmt.Errorf("open worktree of %s: %w", dir, err)
	}

	// go-git's Pull cannot narrow its fetch: it takes every branch the refspec of the remote names and reports
	// a pull whenever any of them moved. Sync fetches main alone and fast-forwards to it itself.
	tracking := plumbing.NewRemoteReferenceName(git.DefaultRemoteName, syncBranch)
	refSpec := config.RefSpec(fmt.Sprintf("+%s:%s", plumbing.NewBranchReferenceName(syncBranch), tracking))
	err = r.FetchContext(ctx, &git.FetchOptions{RemoteName: git.DefaultRemoteName, RefSpecs: []config.RefSpec{refSpec}})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return SyncPulled, fmt.Errorf("pull %s %s into %s: %w", git.DefaultRemoteName, syncBranch, dir, err)
	}
	fetched, err := r.Reference(tracking, true)
	if err != nil {
		return SyncPulled, fmt.Errorf("read %s in %s: %w", tracking, dir, err)
	}

	head, err := r.Head()
	switch {
	case errors.Is(err, plumbing.ErrReferenceNotFound):
		// HEAD names a branch without commits, which Reset cannot move; the branch starts at main.
		var sym *plumbing.Reference
		if sym, err = r.Reference(plumbing.HEAD, false); err == nil {
			err = r.Storer.SetReference(plumbing.NewHashReference(sym.Target(), fetched.Hash()))
		}
	case err != nil:
		// Reported below.
	case head.Hash() == fetched.Hash():
		return SyncUpToDate, nil
	case contains(r, fetched.Hash(), head.Hash()):
		// A fast-forward.
	case contains(r, head.Hash(), fetched.Hash()):
		// Local commits on top of main leave nothing to merge, as `git pull` sees it.
		return SyncUpToDate, nil
	default:
		err = git.ErrNonFastForwardUpdate
	}
	if err != nil {
		return SyncPulled, fmt.Errorf("merge %s into %s: %w", tracking, dir, err)
	}
	// Reset refuses uncommitted changes before it moves the branch HEAD names, then the index and the files.
	if err := wt.Reset(&git.ResetOptions{Mode: git.MergeReset, Commit: fetched.Hash()}); err != nil {
		return SyncPulled, fmt.Errorf("check out %s in %s: %w", tracking, dir, err)
	}
	return SyncPulled, nil
}

// contains reports whether commit is tip or one of its ancestors. A history that cannot be read counts as not
// containing it.
func contains(r *git.Repository, tip, commit plumbing.Hash) bool {
	t, terr := r.CommitObject(tip)
	c, cerr := r.CommitObject(commit)
	if terr != nil || cerr != nil {
		return false
	}
	ok, err := c.IsAncestor(t)
	return err == nil && ok
}
