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
// is true, a directory that is not a git repository is left alone, and a repository has the main branch of
// its origin remote merged in; only a fast-forward is possible. ctx bounds the network transfer.
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
	err = wt.PullContext(ctx, &git.PullOptions{
		RemoteName:    git.DefaultRemoteName,
		ReferenceName: plumbing.NewBranchReferenceName(syncBranch),
	})
	switch {
	case errors.Is(err, git.NoErrAlreadyUpToDate):
		return SyncUpToDate, nil
	case err != nil:
		return SyncPulled, fmt.Errorf("pull %s %s into %s: %w", git.DefaultRemoteName, syncBranch, dir, err)
	}
	return SyncPulled, nil
}
