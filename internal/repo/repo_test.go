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
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/x/plugin/objectsigner/program"
	gocmp "github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// probeFixed returns a ProbeIgnoreCase that reports ignoreCase without touching the file system.
func probeFixed(ignoreCase bool) func(string) (bool, error) {
	return func(string) (bool, error) { return ignoreCase, nil }
}

// openTestRepo writes files into a new directory and opens a repository there with the ok.sh signer.
func openTestRepo(t *testing.T, files map[string]string) (*Repository, string) {
	t.Helper()
	dir := t.TempDir()
	writeFiles(t, dir, files)
	signer, err := NewSigner(t.Context(), stubConfig(t, "ok.sh"))
	if err != nil {
		t.Fatalf("NewSigner() error = %v", err)
	}
	r, existed, err := Open(Options{
		Dir:             dir,
		Config:          testConfig,
		Signer:          signer,
		Now:             func() time.Time { return fixedNow },
		ProbeIgnoreCase: probeFixed(false),
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if existed {
		t.Fatal("Open() existed = true for a new directory")
	}
	return r, dir
}

// history returns the commits reachable from HEAD, oldest first, read with a fresh go-git handle.
func history(t *testing.T, dir string) []*object.Commit {
	t.Helper()
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatalf("PlainOpen() error = %v", err)
	}
	head, err := repo.Head()
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return nil
	}
	if err != nil {
		t.Fatalf("Head() error = %v", err)
	}
	c, err := repo.CommitObject(head.Hash())
	if err != nil {
		t.Fatalf("CommitObject() error = %v", err)
	}
	var out []*object.Commit
	for {
		out = append(out, c)
		if c.NumParents() == 0 {
			break
		}
		if c, err = c.Parent(0); err != nil {
			t.Fatalf("Parent() error = %v", err)
		}
	}
	slices.Reverse(out)
	return out
}

// treeFiles lists every file path in the tree of c, sorted.
func treeFiles(t *testing.T, c *object.Commit) []string {
	t.Helper()
	iter, err := c.Files()
	if err != nil {
		t.Fatalf("Files() error = %v", err)
	}
	var names []string
	if err := iter.ForEach(func(f *object.File) error {
		names = append(names, f.Name)
		return nil
	}); err != nil {
		t.Fatalf("ForEach() error = %v", err)
	}
	slices.Sort(names)
	return names
}

// headRef returns the target of the symbolic HEAD, read from the file itself.
func headRef(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, ".git", "HEAD"))
	if err != nil {
		t.Fatalf("read HEAD: %v", err)
	}
	return string(b)
}

// readGitConfig parses <dir>/.git/config.
func readGitConfig(t *testing.T, dir string) *config.Config {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, ".git", "config"))
	if err != nil {
		t.Fatalf("open .git/config: %v", err)
	}
	defer f.Close()
	cfg, err := config.ReadConfig(f)
	if err != nil {
		t.Fatalf("parse .git/config: %v", err)
	}
	return cfg
}

// planSteps are the commit steps of git-gen, in order.
var planSteps = []CommitStep{
	{Message: "Initial commit", Paths: []string{".gitignore", ".gitattributes", "LICENSE", "CODE_OF_CONDUCT.md"}},
	{Message: "github: add .github directory", Paths: []string{".github/PULL_REQUEST_TEMPLATE.md"}},
	{Message: "go.mod: init module", Paths: []string{"go.mod", "go.sum"}, Force: true},
}

// goGitignore stands for the .gitignore of `git-gen apache2 go`; none of the paths of planSteps match it.
const goGitignore = `# git-gen project generated files to ignore

# github/gitignore/Go
*.exe
*.test
*.out
vendor/
`

// allowlistGitignore is the .gitignore of `git-gen apache2 community/Golang`: the header, then the
// community/Golang allowlist after the four edits git-gen makes to it.
const allowlistGitignore = `# git-gen project generated files to ignore
#  If you want to ignore files created by your editor/tools,
#  please consider a global .gitignore https://docs.github.com/en/get-started/git-basics/ignoring-files.
#  PLEASE DO NOT open a pull request to add something created by your editor or tools

# github/gitignore/community/Golang
# Ignore everything
*

!/.gitattributes
!/.gitignore

!*.go
!go.sum
!go.mod

!README.md
!LICENSE

!Makefile

!*/
`

// generatedFiles is what git-gen writes for a Go project, with .gitignore given by the caller.
func generatedFiles(gitignore string) map[string]string {
	return map[string]string{
		".gitignore":                          gitignore,
		".gitattributes":                      "# git-gen project gitattributes file\n\n* text=auto eol=lf\n",
		"LICENSE":                             "Apache License\nVersion 2.0, January 2004\n",
		"CODE_OF_CONDUCT.md":                  "# Contributor Covenant Code of Conduct\n",
		"README.md":                           "# project\n",
		"Makefile":                            "all:\n",
		".golangci.yaml":                      "version: \"2\"\n",
		".github/PULL_REQUEST_TEMPLATE.md":    "## Summary\n",
		".github/CODEOWNERS":                  "* @git-gen\n",
		"hack/boilerplate/boilerplate.go.txt": "// Copyright YEAR AUTHOR\n",
		"go.mod":                              "module example.com/project\n\ngo 1.27\n",
		"go.sum":                              "",
	}
}

type stepWant struct {
	staged, skipped []string
	tree            []string // files of HEAD after the step; nil when the step makes no commit
}

// TestCommit checks the commits that planSteps make, and that running them again commits nothing.
func TestCommit(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		files    map[string]string
		remove   []string // generated files deleted before the steps run
		prestage []string // files staged before the steps, as a user might
		want     []stepWant
		// wantStatus is `git status --porcelain` after the steps, sorted.
		wantStatus []string
	}{
		"success: apache2 go makes the initial, .github and go.mod commits": {
			files: generatedFiles(goGitignore),
			want: []stepWant{
				{
					staged: []string{".gitignore", ".gitattributes", "LICENSE", "CODE_OF_CONDUCT.md"},
					tree:   []string{".gitattributes", ".gitignore", "CODE_OF_CONDUCT.md", "LICENSE"},
				},
				{
					staged: []string{".github/PULL_REQUEST_TEMPLATE.md"},
					tree:   []string{".gitattributes", ".github/PULL_REQUEST_TEMPLATE.md", ".gitignore", "CODE_OF_CONDUCT.md", "LICENSE"},
				},
				{
					staged: []string{"go.mod", "go.sum"},
					tree:   []string{".gitattributes", ".github/PULL_REQUEST_TEMPLATE.md", ".gitignore", "CODE_OF_CONDUCT.md", "LICENSE", "go.mod", "go.sum"},
				},
			},
			wantStatus: []string{"?? .github/CODEOWNERS", "?? .golangci.yaml", "?? Makefile", "?? README.md", "?? hack/"},
		},
		"success: community/Golang skips the paths its allowlist ignores": {
			files: generatedFiles(allowlistGitignore),
			want: []stepWant{
				{
					staged:  []string{".gitignore", ".gitattributes", "LICENSE"},
					skipped: []string{"CODE_OF_CONDUCT.md"},
					tree:    []string{".gitattributes", ".gitignore", "LICENSE"},
				},
				{
					skipped: []string{".github/PULL_REQUEST_TEMPLATE.md"},
				},
				{
					staged: []string{"go.mod", "go.sum"},
					tree:   []string{".gitattributes", ".gitignore", "LICENSE", "go.mod", "go.sum"},
				},
			},
			wantStatus: []string{"?? Makefile", "?? README.md"},
		},
		"success: missing paths are left out and make no commit": {
			files:  generatedFiles(goGitignore),
			remove: []string{".gitattributes", "CODE_OF_CONDUCT.md", ".github", "go.mod", "go.sum"},
			want: []stepWant{
				{
					staged: []string{".gitignore", "LICENSE"},
					tree:   []string{".gitignore", "LICENSE"},
				},
				{},
				{},
			},
			wantStatus: []string{"?? .golangci.yaml", "?? Makefile", "?? README.md", "?? hack/"},
		},
		"success: a path below a regular file is left out like a missing one": {
			files: func() map[string]string {
				files := generatedFiles(goGitignore)
				delete(files, ".github/PULL_REQUEST_TEMPLATE.md")
				delete(files, ".github/CODEOWNERS")
				files[".github"] = "not a directory\n"
				return files
			}(),
			want: []stepWant{
				{
					staged: []string{".gitignore", ".gitattributes", "LICENSE", "CODE_OF_CONDUCT.md"},
					tree:   []string{".gitattributes", ".gitignore", "CODE_OF_CONDUCT.md", "LICENSE"},
				},
				{},
				{
					staged: []string{"go.mod", "go.sum"},
					tree:   []string{".gitattributes", ".gitignore", "CODE_OF_CONDUCT.md", "LICENSE", "go.mod", "go.sum"},
				},
			},
			wantStatus: []string{"?? .github", "?? .golangci.yaml", "?? Makefile", "?? README.md", "?? hack/"},
		},
		"success: a file staged beforehand goes into the first commit": {
			files:    generatedFiles(goGitignore),
			prestage: []string{"README.md"},
			want: []stepWant{
				{
					staged: []string{".gitignore", ".gitattributes", "LICENSE", "CODE_OF_CONDUCT.md"},
					tree:   []string{".gitattributes", ".gitignore", "CODE_OF_CONDUCT.md", "LICENSE", "README.md"},
				},
				{
					staged: []string{".github/PULL_REQUEST_TEMPLATE.md"},
					tree:   []string{".gitattributes", ".github/PULL_REQUEST_TEMPLATE.md", ".gitignore", "CODE_OF_CONDUCT.md", "LICENSE", "README.md"},
				},
				{
					staged: []string{"go.mod", "go.sum"},
					tree:   []string{".gitattributes", ".github/PULL_REQUEST_TEMPLATE.md", ".gitignore", "CODE_OF_CONDUCT.md", "LICENSE", "README.md", "go.mod", "go.sum"},
				},
			},
			wantStatus: []string{"?? .github/CODEOWNERS", "?? .golangci.yaml", "?? Makefile", "?? hack/"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r, dir := openTestRepo(t, tt.files)
			for _, p := range tt.remove {
				if err := os.RemoveAll(filepath.Join(dir, p)); err != nil {
					t.Fatal(err)
				}
			}
			if len(tt.prestage) > 0 {
				wt, err := r.repo.Worktree()
				if err != nil {
					t.Fatal(err)
				}
				for _, p := range tt.prestage {
					if _, err := wt.Add(p); err != nil {
						t.Fatalf("prestage %s: %v", p, err)
					}
				}
			}

			var wantSubjects []string
			for i, step := range planSteps {
				res, err := r.Commit(t.Context(), step)
				if err != nil {
					t.Fatalf("step %d Commit() error = %v", i+1, err)
				}
				want := tt.want[i]
				if diff := gocmp.Diff(want.staged, res.Staged, cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("step %d Staged mismatch (-want +got):\n%s", i+1, diff)
				}
				if diff := gocmp.Diff(want.skipped, res.Skipped, cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("step %d Skipped mismatch (-want +got):\n%s", i+1, diff)
				}
				if got := res.Hash != ""; got != (want.tree != nil) {
					t.Errorf("step %d Hash = %q, want a commit: %t", i+1, res.Hash, want.tree != nil)
				}
				if want.tree != nil {
					wantSubjects = append(wantSubjects, step.Message)
				}
			}

			commits := history(t, dir)
			var gotSubjects []string
			var wantTrees, gotTrees [][]string
			for _, w := range tt.want {
				if w.tree != nil {
					wantTrees = append(wantTrees, w.tree)
				}
			}
			for _, c := range commits {
				subject, ok := strings.CutSuffix(c.Message, "\n")
				if !ok || strings.Contains(subject, "\n") {
					t.Errorf("commit %s message = %q, want one line ending in a newline", c.Hash, c.Message)
				}
				gotSubjects = append(gotSubjects, subject)
				gotTrees = append(gotTrees, treeFiles(t, c))

				if c.Signature != stubSignature {
					t.Errorf("commit %s signature = %q, want the ok.sh block", c.Hash, c.Signature)
				}
				wantAuthor := object.Signature{Name: testConfig.Author.Name, Email: testConfig.Author.Email, When: fixedNow}
				wantCommitter := object.Signature{Name: testConfig.Committer.Name, Email: testConfig.Committer.Email, When: fixedNow}
				if diff := gocmp.Diff(wantAuthor, c.Author); diff != "" {
					t.Errorf("commit %s author mismatch (-want +got):\n%s", c.Hash, diff)
				}
				if diff := gocmp.Diff(wantCommitter, c.Committer); diff != "" {
					t.Errorf("commit %s committer mismatch (-want +got):\n%s", c.Hash, diff)
				}
				_, wantOffset := fixedNow.Zone()
				if _, off := c.Author.When.Zone(); off != wantOffset {
					t.Errorf("commit %s author offset = %d, want %d", c.Hash, off, wantOffset)
				}
			}
			if diff := gocmp.Diff(wantSubjects, gotSubjects); diff != "" {
				t.Errorf("subjects mismatch (-want +got):\n%s", diff)
			}
			if diff := gocmp.Diff(wantTrees, gotTrees); diff != "" {
				t.Errorf("trees mismatch (-want +got):\n%s", diff)
			}

			// The same steps again make no commit.
			for i, step := range planSteps {
				res, err := r.Commit(t.Context(), step)
				if err != nil {
					t.Fatalf("rerun step %d Commit() error = %v", i+1, err)
				}
				if res.Hash != "" {
					t.Errorf("rerun step %d made commit %s, want none", i+1, res.Hash)
				}
			}
			if got := len(history(t, dir)); got != len(commits) {
				t.Errorf("after the rerun there are %d commits, want %d", got, len(commits))
			}

			gitCLI(t, dir, "fsck", "--strict")
			var status []string
			for line := range strings.SplitSeq(strings.TrimSpace(gitCLI(t, dir, "status", "--porcelain=v1")), "\n") {
				if line != "" {
					status = append(status, line)
				}
			}
			slices.Sort(status)
			if diff := gocmp.Diff(tt.wantStatus, status, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("git status mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestCommitTrackedIgnored checks that the ignore rules apply to untracked paths only, as with git add: a
// committed file that a new .gitignore ignores is still staged.
func TestCommitTrackedIgnored(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		step        CommitStep
		wantStaged  []string
		wantSkipped []string
		// wantStatus is `git status --porcelain` after the step, sorted.
		wantStatus []string
	}{
		"success: a tracked path is staged although .gitignore now ignores it": {
			step:       CommitStep{Message: "Initial commit", Paths: []string{".gitignore", "CODE_OF_CONDUCT.md"}},
			wantStaged: []string{".gitignore", "CODE_OF_CONDUCT.md"},
			wantStatus: []string{"?? Makefile", "?? README.md", "?? go.mod", "?? go.sum", "M  .gitignore", "M  CODE_OF_CONDUCT.md"},
		},
		"success: an untracked path that .gitignore ignores is still skipped": {
			step:        CommitStep{Message: "github: add .github directory", Paths: []string{".github/PULL_REQUEST_TEMPLATE.md"}},
			wantSkipped: []string{".github/PULL_REQUEST_TEMPLATE.md"},
			wantStatus:  []string{" M .gitignore", " M CODE_OF_CONDUCT.md", "?? Makefile", "?? README.md", "?? go.mod", "?? go.sum"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r, dir := openTestRepo(t, generatedFiles(goGitignore))
			if res, err := r.Commit(t.Context(), planSteps[0]); err != nil || res.Hash == "" {
				t.Fatalf("first Commit() = %+v, %v, want a commit", res, err)
			}
			writeFiles(t, dir, map[string]string{
				".gitignore":         allowlistGitignore,
				"CODE_OF_CONDUCT.md": "# Contributor Covenant Code of Conduct, edited\n",
			})

			res, err := r.Commit(t.Context(), tt.step)
			if err != nil {
				t.Fatalf("Commit() error = %v", err)
			}
			if diff := gocmp.Diff(tt.wantStaged, res.Staged, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Staged mismatch (-want +got):\n%s", diff)
			}
			if diff := gocmp.Diff(tt.wantSkipped, res.Skipped, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Skipped mismatch (-want +got):\n%s", diff)
			}
			var status []string
			for line := range strings.SplitSeq(strings.TrimRight(gitCLI(t, dir, "status", "--porcelain=v1"), "\n"), "\n") {
				status = append(status, line)
			}
			slices.Sort(status)
			if diff := gocmp.Diff(tt.wantStatus, status); diff != "" {
				t.Errorf("git status mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// sleepingStub writes a signing program that records its start in marker and then sleeps far longer
// than any test waits.
func sleepingStub(t *testing.T, marker string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sleep.sh")
	script := "#!/bin/sh\n: > '" + marker + "'\nexec sleep 30\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestCommitContext checks that the caller's context reaches the signing program even though go-git
// calls Sign with context.TODO().
func TestCommitContext(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		cancelEarly bool // cancel before Commit instead of while the program runs
		wantStarted bool
	}{
		"error: a context cancelled before Commit never starts the program": {
			cancelEarly: true,
			wantStarted: false,
		},
		"error: a context cancelled while signing stops the program": {
			cancelEarly: false,
			wantStarted: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			marker := filepath.Join(t.TempDir(), "started")
			signer, err := program.New(program.FormatOpenPGP, sleepingStub(t, marker), "KEY")
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			writeFiles(t, dir, map[string]string{"LICENSE": "license\n"})
			r, _, err := Open(Options{Dir: dir, Config: testConfig, Signer: signer, Now: func() time.Time { return fixedNow }, ProbeIgnoreCase: probeFixed(false)})
			if err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.cancelEarly {
				cancel()
			} else {
				go func() {
					defer cancel()
					for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
						if _, err := os.Stat(marker); err == nil {
							return
						}
					}
				}()
			}

			start := time.Now()
			res, err := r.Commit(ctx, CommitStep{Message: "Initial commit", Paths: []string{"LICENSE"}})
			elapsed := time.Since(start)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Commit() error = %v, want errors.Is(context.Canceled)", err)
			}
			if elapsed > 15*time.Second {
				t.Errorf("Commit() returned after %s; the program was not stopped", elapsed)
			}
			if res.Hash != "" {
				t.Errorf("Commit() Hash = %q, want none", res.Hash)
			}
			if _, statErr := os.Stat(marker); (statErr == nil) != tt.wantStarted {
				t.Errorf("program started = %t, want %t", statErr == nil, tt.wantStarted)
			}
			if got := history(t, dir); len(got) != 0 {
				t.Errorf("history has %d commits, want 0", len(got))
			}
		})
	}
}

// TestCommitErrors covers the failures of Commit. None of them may leave a commit behind.
func TestCommitErrors(t *testing.T) {
	t.Parallel()

	emptySigner := signerFunc(func(context.Context, io.Reader) ([]byte, error) { return nil, nil })
	tests := map[string]struct {
		signer func(t *testing.T) Signer
		cfg    Config
		step   CommitStep
		// prepare changes the repository after Open; nil changes nothing.
		prepare   func(t *testing.T, dir string)
		wantErr   string
		wantIs    error
		wantNoLog bool // the error must carry no gpg status line
	}{
		"error: an empty signature is ErrUnsigned": {
			signer: func(*testing.T) Signer { return emptySigner },
			cfg:    testConfig,
			step:   CommitStep{Message: "Initial commit", Paths: []string{"LICENSE"}},
			wantIs: ErrUnsigned,
		},
		"error: a failing program leaks no status line and no key": {
			signer: func(t *testing.T) Signer {
				t.Helper()
				s, err := program.New(program.FormatOpenPGP, testdataPath(t, "signer", "fail.sh"), testConfig.SigningKey)
				if err != nil {
					t.Fatal(err)
				}
				return s
			},
			cfg:       testConfig,
			step:      CommitStep{Message: "Initial commit", Paths: []string{"LICENSE"}},
			wantErr:   `commit "Initial commit"`,
			wantNoLog: true,
		},
		"error: no signer": {
			signer:  func(*testing.T) Signer { return nil },
			cfg:     testConfig,
			step:    CommitStep{Message: "Initial commit", Paths: []string{"LICENSE"}},
			wantErr: "no signer",
		},
		"error: incomplete identity": {
			signer:  func(*testing.T) Signer { return emptySigner },
			cfg:     Config{Author: testConfig.Author},
			step:    CommitStep{Message: "Initial commit", Paths: []string{"LICENSE"}},
			wantErr: "identity is incomplete",
		},
		"error: empty message": {
			signer:  func(*testing.T) Signer { return emptySigner },
			cfg:     testConfig,
			step:    CommitStep{Message: " \n", Paths: []string{"LICENSE"}},
			wantErr: "empty message",
		},
		"error: a path outside the working tree": {
			signer:  func(*testing.T) Signer { return emptySigner },
			cfg:     testConfig,
			step:    CommitStep{Message: "Initial commit", Paths: []string{"../outside"}},
			wantErr: "not a clean relative path",
		},
		"error: a path that is not clean": {
			signer:  func(*testing.T) Signer { return emptySigner },
			cfg:     testConfig,
			step:    CommitStep{Message: "Initial commit", Paths: []string{"./LICENSE"}},
			wantErr: "not a clean relative path",
		},
		"error: a corrupt index": {
			signer: func(*testing.T) Signer { return emptySigner },
			cfg:    testConfig,
			step:   CommitStep{Message: "Initial commit", Paths: []string{"LICENSE"}},
			prepare: func(t *testing.T, dir string) {
				t.Helper()
				writeFiles(t, dir, map[string]string{".git/index": "not an index\n"})
			},
			wantErr: "read index",
		},
		"error: an unreadable .gitignore": {
			signer:  func(*testing.T) Signer { return emptySigner },
			cfg:     testConfig,
			step:    CommitStep{Message: "Initial commit", Paths: []string{"sub/file"}},
			wantErr: "read .gitignore",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeFiles(t, dir, map[string]string{"LICENSE": "license\n", "sub/file": "x\n"})
			if err := os.MkdirAll(filepath.Join(dir, "sub", ".gitignore"), 0o755); err != nil {
				t.Fatal(err)
			}
			r, _, err := Open(Options{Dir: dir, Config: tt.cfg, Signer: tt.signer(t), Now: func() time.Time { return fixedNow }, ProbeIgnoreCase: probeFixed(false)})
			if err != nil {
				t.Fatal(err)
			}
			if tt.prepare != nil {
				tt.prepare(t, dir)
			}
			res, err := r.Commit(t.Context(), tt.step)
			if err == nil {
				t.Fatal("Commit() error = nil, want an error")
			}
			if tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Commit() error = %q, want it to contain %q", err, tt.wantErr)
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("Commit() error = %v, want errors.Is(%v)", err, tt.wantIs)
			}
			if tt.wantNoLog {
				assertNoStatusLines(t, err)
				assertNoKey(t, err, tt.cfg.SigningKey)
			}
			if res.Hash != "" {
				t.Errorf("Commit() Hash = %q, want none", res.Hash)
			}
			if got := history(t, dir); len(got) != 0 {
				t.Errorf("history has %d commits after the error, want 0", len(got))
			}
		})
	}
}

// TestCheckDir checks the .git of a working tree before its configuration is read. The owner is injected,
// because a test cannot create a file that another user owns; owner_unix_test.go covers the real lookup.
func TestCheckDir(t *testing.T) {
	t.Parallel()

	const uid = 1000
	owners := func(gitDir, config int) func(fs.FileInfo) (int, bool) {
		return func(fi fs.FileInfo) (int, bool) {
			if fi.Name() == "config" {
				return config, true
			}
			return gitDir, true
		}
	}
	repoDir := func(t *testing.T, dir string) string {
		t.Helper()
		writeFiles(t, dir, map[string]string{".git/config": "[core]\n\tbare = false\n"})
		return dir
	}
	tests := map[string]struct {
		// setup prepares the temporary directory root and returns the working tree to check.
		setup func(t *testing.T, root string) string
		owner func(fs.FileInfo) (int, bool)
		// wantErr is the start of the message, with {dir} standing for the working tree; empty means nil.
		wantErr string
	}{
		"success: no .git": {
			setup: func(_ *testing.T, root string) string { return root },
			owner: owners(uid+1, uid+1),
		},
		"success: .git and .git/config of the current user": {
			setup: repoDir,
			owner: owners(uid, uid),
		},
		"success: a .git directory without config": {
			setup: func(t *testing.T, dir string) string {
				t.Helper()
				if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			owner: owners(uid, uid+1),
		},
		"success: files without an owner on this system": {
			setup: repoDir,
			owner: func(fs.FileInfo) (int, bool) { return 0, false },
		},
		"error: a .git file": {
			setup: func(t *testing.T, dir string) string {
				t.Helper()
				writeFiles(t, dir, map[string]string{".git": "gitdir: ../other/.git\n"})
				return dir
			},
			owner:   owners(uid, uid),
			wantErr: "{dir}/.git is a file, not a directory: linked worktrees and submodules are not supported",
		},
		"error: a .git link to a directory": {
			setup: func(t *testing.T, dir string) string {
				t.Helper()
				target := repoDir(t, t.TempDir())
				if err := os.Symlink(filepath.Join(target, ".git"), filepath.Join(dir, ".git")); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			owner:   owners(uid, uid),
			wantErr: "{dir}/.git is not a directory",
		},
		"error: .git of another user": {
			setup:   repoDir,
			owner:   owners(uid+1, uid),
			wantErr: "{dir}/.git is owned by uid 1001, not by the current user (uid 1000)",
		},
		"error: .git/config of another user": {
			setup:   repoDir,
			owner:   owners(uid, uid+1),
			wantErr: "{dir}/.git/config is owned by uid 1001, not by the current user (uid 1000)",
		},
		"error: a .git the system cannot look up": {
			setup: func(_ *testing.T, root string) string {
				return filepath.Join(root, strings.Repeat("n", 300)) // longer than any file name may be
			},
			owner:   owners(uid, uid),
			wantErr: "look for {dir}/.git: ",
		},
		"error: a .git/config that links to itself": {
			setup: func(t *testing.T, dir string) string {
				t.Helper()
				if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("config", filepath.Join(dir, ".git", "config")); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			owner:   owners(uid, uid),
			wantErr: "look for {dir}/.git/config: ",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := tt.setup(t, root)
			before := dirSnapshot(t, root)

			err := checkDir(dir, uid, tt.owner)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("checkDir() error = %v", err)
				}
			} else {
				want := strings.ReplaceAll(filepath.FromSlash(tt.wantErr), "{dir}", dir)
				if err == nil || !strings.HasPrefix(err.Error(), want) {
					t.Fatalf("checkDir() error = %v, want one starting with %q", err, want)
				}
			}
			if diff := gocmp.Diff(before, dirSnapshot(t, root)); diff != "" {
				t.Errorf("checkDir() changed the working tree (-before +after):\n%s", diff)
			}
		})
	}
}

// dirSnapshot lists every path under dir with its size and mode, without following links.
func dirSnapshot(t *testing.T, dir string) []string {
	t.Helper()
	var entries []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		entries = append(entries, fmt.Sprintf("%s %s %d", p, fi.Mode(), fi.Size()))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// TestOpen checks the initial branch and core.ignorecase of a new repository.
func TestOpen(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		branch         string
		probe          func(string) (bool, error)
		wantHEAD       string
		wantIgnoreCase bool
		wantErr        string
	}{
		"success: HEAD is the configured default branch": {
			branch:   "trunk",
			probe:    probeFixed(false),
			wantHEAD: "ref: refs/heads/trunk\n",
		},
		"success: HEAD is main when no branch is configured": {
			probe:    probeFixed(false),
			wantHEAD: "ref: refs/heads/main\n",
		},
		"success: a probe that reports true writes core.ignorecase": {
			branch:         "main",
			probe:          probeFixed(true),
			wantHEAD:       "ref: refs/heads/main\n",
			wantIgnoreCase: true,
		},
		"error: the probe fails": {
			probe:   func(string) (bool, error) { return false, errors.New("probe broke") },
			wantErr: "probe broke",
		},
		"error: an invalid branch name": {
			branch:  "bad..name",
			probe:   probeFixed(false),
			wantErr: "initialize repository",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			cfg := testConfig
			cfg.DefaultBranch = tt.branch
			var probed string
			probe := func(d string) (bool, error) { probed = d; return tt.probe(d) }
			r, existed, err := Open(Options{Dir: dir, Config: cfg, ProbeIgnoreCase: probe})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Open() error = %v, want an error containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Open() error = %v", err)
			}
			if r == nil || existed {
				t.Fatalf("Open() = %v, %t, want a repository that did not exist", r, existed)
			}
			if want := filepath.Join(dir, ".git"); probed != want {
				t.Errorf("probe ran on %q, want %q", probed, want)
			}
			if got := headRef(t, dir); got != tt.wantHEAD {
				t.Errorf("HEAD = %q, want %q", got, tt.wantHEAD)
			}
			core := readGitConfig(t, dir).Raw.Section("core")
			if got := core.HasOption("ignorecase"); got != tt.wantIgnoreCase {
				t.Errorf("core.ignorecase present = %t, want %t", got, tt.wantIgnoreCase)
			}
			if tt.wantIgnoreCase && core.Option("ignorecase") != "true" {
				t.Errorf("core.ignorecase = %q, want true", core.Option("ignorecase"))
			}
			gitCLI(t, dir, "status", "--porcelain=v1")
		})
	}
}

// TestOpenIgnoreCaseKeepsConfig checks that writing core.ignorecase leaves every other key alone.
func TestOpenIgnoreCaseKeepsConfig(t *testing.T) {
	t.Parallel()

	dirs := map[bool]string{false: t.TempDir(), true: t.TempDir()}
	for ignoreCase, dir := range dirs {
		if _, _, err := Open(Options{Dir: dir, Config: testConfig, ProbeIgnoreCase: probeFixed(ignoreCase)}); err != nil {
			t.Fatalf("Open(ignoreCase=%t) error = %v", ignoreCase, err)
		}
	}
	plain, withKey := readGitConfig(t, dirs[false]).Raw, readGitConfig(t, dirs[true]).Raw
	withKey.Section("core").RemoveOption("ignorecase")
	if diff := gocmp.Diff(plain, withKey); diff != "" {
		t.Errorf(".git/config differs beyond core.ignorecase (-plain +with key):\n%s", diff)
	}
}

func TestOpenExisting(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		// setup prepares dir and returns the directory to open.
		setup   func(t *testing.T, dir string) string
		wantRef string
		wantErr string
	}{
		"success: an existing repository is opened, not initialized": {
			setup: func(t *testing.T, dir string) string {
				t.Helper()
				if _, err := git.PlainInit(dir, false, git.WithDefaultBranch(plumbing.NewBranchReferenceName("legacy"))); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			wantRef: "ref: refs/heads/legacy\n",
		},
		"error: the directory is a regular file": {
			setup: func(t *testing.T, dir string) string {
				t.Helper()
				writeFiles(t, dir, map[string]string{"file": ""})
				return filepath.Join(dir, "file")
			},
			wantErr: "look for",
		},
		"error: a .git file without a gitdir line": {
			setup: func(t *testing.T, dir string) string {
				t.Helper()
				writeFiles(t, dir, map[string]string{".git": "not a gitfile\n"})
				return dir
			},
			wantErr: "open repository",
		},
		"error: a broken .git directory": {
			setup: func(t *testing.T, dir string) string {
				t.Helper()
				if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			wantErr: "open repository",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := tt.setup(t, t.TempDir())
			probe := func(string) (bool, error) {
				t.Error("the probe ran on an existing repository")
				return true, nil
			}
			r, existed, err := Open(Options{Dir: dir, Config: testConfig, ProbeIgnoreCase: probe})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Open() error = %v, want an error containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Open() error = %v", err)
			}
			if r == nil || !existed {
				t.Fatalf("Open() = %v, %t, want an existing repository", r, existed)
			}
			if got := headRef(t, dir); got != tt.wantRef {
				t.Errorf("HEAD = %q, want %q", got, tt.wantRef)
			}
			if readGitConfig(t, dir).Raw.Section("core").HasOption("ignorecase") {
				t.Error("core.ignorecase was written into an existing repository")
			}
		})
	}
}

// TestProbeIgnoreCase runs the real probe and compares it with an independent check made by hand.
func TestProbeIgnoreCase(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	got, err := probeIgnoreCase(dir)
	if err != nil {
		t.Fatalf("probeIgnoreCase() error = %v", err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("probe left %d entries behind (err %v)", len(entries), err)
	}

	oracle := t.TempDir()
	if err := os.WriteFile(filepath.Join(oracle, "lower"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = os.Stat(filepath.Join(oracle, "LOWER"))
	if want := err == nil; got != want {
		t.Errorf("probeIgnoreCase() = %t, want %t", got, want)
	}

	if _, err := probeIgnoreCase(filepath.Join(dir, "missing")); err == nil {
		t.Error("probeIgnoreCase() on a missing directory: error = nil")
	}

	// Open with a nil probe uses the real one.
	repoDir := t.TempDir()
	if _, _, err := Open(Options{Dir: repoDir, Config: testConfig}); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if written := readGitConfig(t, repoDir).Raw.Section("core").HasOption("ignorecase"); written != got {
		t.Errorf("Open() with the real probe wrote core.ignorecase: %t, want %t", written, got)
	}
}

func TestSwapCase(t *testing.T) {
	t.Parallel()

	tests := map[string]struct{ in, want string }{
		"success: letters swap":        {in: "CaseProbe123", want: "cASEpROBE123"},
		"success: non-letters survive": {in: "1-2_3", want: "1-2_3"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := swapCase(tt.in); got != tt.want {
				t.Errorf("swapCase(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestAddRemote checks that origin is added and that an existing origin is left unchanged.
func TestAddRemote(t *testing.T) {
	t.Parallel()

	const origin = "git@github.com:org/project.git"
	tests := map[string]struct {
		before      map[string]string // remotes added first, by name
		remote, url string
		wantURL     string
		wantAdded   bool
		wantErr     string
	}{
		"success: origin is added": {
			remote: "origin", url: origin,
			wantURL: origin, wantAdded: true,
		},
		"success: an existing origin is kept": {
			before: map[string]string{"origin": origin},
			remote: "origin", url: "git@github.com:other/project.git",
			wantURL: origin, wantAdded: false,
		},
		"success: another name is added next to origin": {
			before: map[string]string{"origin": origin},
			remote: "upstream", url: "https://github.com/org/project.git",
			wantURL: "https://github.com/org/project.git", wantAdded: true,
		},
		"error: an empty name": {
			remote: "", url: origin,
			wantErr: "add remote",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r, dir := openTestRepo(t, nil)
			for n, u := range tt.before {
				if _, _, err := r.AddRemote(n, u); err != nil {
					t.Fatal(err)
				}
			}
			gotURL, added, err := r.AddRemote(tt.remote, tt.url)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("AddRemote() error = %v, want an error containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("AddRemote() error = %v", err)
			}
			if gotURL != tt.wantURL || added != tt.wantAdded {
				t.Errorf("AddRemote() = %q, %t, want %q, %t", gotURL, added, tt.wantURL, tt.wantAdded)
			}

			cfg := readGitConfig(t, dir)
			rc, ok := cfg.Remotes[tt.remote]
			if !ok {
				t.Fatalf("remote %q is not in .git/config", tt.remote)
			}
			if diff := gocmp.Diff([]string{tt.wantURL}, rc.URLs); diff != "" {
				t.Errorf("remote URLs mismatch (-want +got):\n%s", diff)
			}
			wantFetch := []config.RefSpec{config.RefSpec("+refs/heads/*:refs/remotes/" + tt.remote + "/*")}
			if diff := gocmp.Diff(wantFetch, rc.Fetch); diff != "" {
				t.Errorf("remote fetch mismatch (-want +got):\n%s", diff)
			}
			gitCLI(t, dir, "status", "--porcelain=v1")
		})
	}
}
