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
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v6"
	gocmp "github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// gitIgnoredFiles asks the git command line which files under dir it ignores. With --ignored and
// --untracked-files=all, git status lists every ignored file on its own "!!" line.
func gitIgnoredFiles(t *testing.T, dir string) []string {
	t.Helper()
	var ignored []string
	for entry := range strings.SplitSeq(gitCLI(t, dir, "status", "--porcelain=v1", "-z", "--ignored", "--untracked-files=all"), "\x00") {
		if p, ok := strings.CutPrefix(entry, "!! "); ok {
			ignored = append(ignored, p)
		}
	}
	slices.Sort(ignored)
	return ignored
}

// TestIgnoreMatcher checks ignoreMatcher against two independent answers: the list of ignored files
// written by hand from the gitignore documentation, and git itself.
func TestIgnoreMatcher(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		files       map[string]string // .gitignore files and the files to check
		wantIgnored []string
	}{
		"success: the community/Golang allowlist": {
			files: map[string]string{
				".gitignore":                          allowlistGitignore,
				".gitattributes":                      "",
				"LICENSE":                             "",
				"README.md":                           "",
				"Makefile":                            "",
				"CODE_OF_CONDUCT.md":                  "",
				".golangci.yaml":                      "",
				".github/PULL_REQUEST_TEMPLATE.md":    "",
				"hack/boilerplate/boilerplate.go.txt": "",
				"cmd/tool/main.go":                    "",
				"go.mod":                              "",
				"go.sum":                              "",
			},
			wantIgnored: []string{
				".github/PULL_REQUEST_TEMPLATE.md", ".golangci.yaml", "CODE_OF_CONDUCT.md",
				"hack/boilerplate/boilerplate.go.txt",
			},
		},
		"success: a file cannot be re-included below an ignored directory": {
			files: map[string]string{
				".gitignore":      "build/\n!build/keep.txt\n",
				"build/keep.txt":  "",
				"build/out.bin":   "",
				"src/build/x.txt": "",
				"build.txt":       "",
			},
			wantIgnored: []string{"build/keep.txt", "build/out.bin", "src/build/x.txt"},
		},
		"success: a pattern without a slash matches the last component only": {
			files: map[string]string{
				".gitignore":  "build\n!build/\n",
				"build/x.txt": "",
				"sub/build":   "",
			},
			wantIgnored: []string{"sub/build"},
		},
		"success: anchored patterns and double stars": {
			files: map[string]string{
				".gitignore":        "/root.txt\ndocs/**/*.md\n**/tmp\nlogs/**\n!logs/keep\n",
				"root.txt":          "",
				"sub/root.txt":      "",
				"docs/a.md":         "",
				"docs/x/y/b.md":     "",
				"docs/x/c.txt":      "",
				"deep/tmp":          "",
				"deep/er/tmp/f":     "",
				"logs/a.log":        "",
				"logs/keep":         "",
				"other/logs/b.log":  "",
				"docs/readme.mdown": "",
			},
			wantIgnored: []string{"deep/er/tmp/f", "deep/tmp", "docs/a.md", "docs/x/y/b.md", "logs/a.log", "root.txt"},
		},
		"success: a nested .gitignore adds to and overrides its parent": {
			files: map[string]string{
				".gitignore":           "*.log\n",
				"sub/.gitignore":       "!keep.log\n/local.txt\n",
				"a.log":                "",
				"sub/keep.log":         "",
				"sub/other.log":        "",
				"sub/local.txt":        "",
				"sub/deeper/local.txt": "",
				"local.txt":            "",
			},
			wantIgnored: []string{"a.log", "sub/local.txt", "sub/other.log"},
		},
		"success: escapes, trailing spaces and wildcards": {
			files: map[string]string{
				".gitignore": "\\#hash\n\\!bang\ntrailing\\ \nspaces   \nfile?.[ch]\n# comment\n\n",
				"#hash":      "",
				"!bang":      "",
				"trailing ":  "",
				"spaces":     "",
				"file1.c":    "",
				"file2.h":    "",
				"file10.c":   "",
				"comment":    "",
			},
			wantIgnored: []string{"!bang", "#hash", "file1.c", "file2.h", "spaces", "trailing "},
		},
		"success: an allowlist with a negated wildcard keeps nested sources": {
			files: map[string]string{
				".gitignore": "*\n!*/\n!*.go\n!/.gitignore\n",
				"a/b/c.go":   "",
				"a/b/c.txt":  "",
				"top.go":     "",
				"notes.md":   "",
			},
			wantIgnored: []string{"a/b/c.txt", "notes.md"},
		},
		"success: a directory-only pattern does not match a file of that name": {
			files: map[string]string{
				".gitignore":   "logs/\n",
				"logs":         "",
				"x/logs/a.txt": "",
			},
			wantIgnored: []string{"x/logs/a.txt"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if _, err := git.PlainInit(dir, false); err != nil {
				t.Fatal(err)
			}
			writeFiles(t, dir, tt.files)

			m := newIgnoreMatcher(dir)
			var got []string
			for p := range tt.files {
				if filepath.Base(p) == ".gitignore" {
					continue
				}
				fi, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(p)))
				if err != nil {
					t.Fatal(err)
				}
				ignored, err := m.ignored(p, fi.IsDir())
				if err != nil {
					t.Fatalf("ignored(%q) error = %v", p, err)
				}
				if ignored {
					got = append(got, p)
				}
			}
			slices.Sort(got)

			if diff := gocmp.Diff(tt.wantIgnored, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("ignored files mismatch with the expected list (-want +got):\n%s", diff)
			}
			if diff := gocmp.Diff(gitIgnoredFiles(t, dir), got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("ignored files mismatch with git (-git +got):\n%s", diff)
			}
		})
	}
}

func TestTrimTrailingSpaces(t *testing.T) {
	t.Parallel()

	tests := map[string]struct{ in, want string }{
		"success: plain spaces are trimmed":   {in: "a  ", want: "a"},
		"success: an escaped space is kept":   {in: `a\ `, want: `a\ `},
		"success: spaces after an escape go":  {in: `a\  `, want: `a\ `},
		"success: inner spaces are kept":      {in: "a b", want: "a b"},
		"success: a trailing backslash stays": {in: `a\`, want: `a\`},
		"success: only spaces become empty":   {in: "   ", want: ""},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := trimTrailingSpaces(tt.in); got != tt.want {
				t.Errorf("trimTrailingSpaces(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseIgnoreLine(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		line   string
		wantOK bool
	}{
		"success: a pattern":               {line: "*.log", wantOK: true},
		"success: a blank line is none":    {line: "", wantOK: false},
		"success: a comment is none":       {line: "# note", wantOK: false},
		"success: a lone slash is none":    {line: "/", wantOK: false},
		"success: a lone bang is none":     {line: "!", wantOK: false},
		"success: a segment with a bang":   {line: "a/!b", wantOK: true},
		"success: an inner trailing space": {line: "a /b", wantOK: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, ok := parseIgnoreLine(tt.line, nil); ok != tt.wantOK {
				t.Errorf("parseIgnoreLine(%q) ok = %t, want %t", tt.line, ok, tt.wantOK)
			}
		})
	}
}
