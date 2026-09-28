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

package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
	"github.com/rogpeppe/go-internal/testscript"

	"github.com/zchee/git-gen/internal/license"
)

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){"git-gen": main})
}

// TestScript runs the scripts under testdata/script. Each script runs git-gen as a separate process in an
// environment that setupScript cuts off from the owner's git configuration, signing key, GitHub token, gh
// binary, boilerplate directory, gitignore checkout and Go environment.
func TestScript(t *testing.T) {
	fixtures, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	goMinor := goMajorMinor(t)

	testscript.Run(t, testscript.Params{
		Dir: filepath.Join("testdata", "script"),
		Setup: func(env *testscript.Env) error {
			return setupScript(env, fixtures, goMinor)
		},
		Cmds: map[string]func(ts *testscript.TestScript, neg bool, args []string){
			"exitcode": cmdExitCode,
			"emptydir": cmdEmptyDir,
			"tree":     cmdTree,
		},
		RequireExplicitExec: true,
		RequireUniqueNames:  true,
	})
}

// goMajorMinor returns the major.minor version that `go env GOVERSION` reports in the environment the
// scripts run in.
func goMajorMinor(t *testing.T) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "go", "env", "GOVERSION")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"GOTOOLCHAIN=local",
		"GOFLAGS=",
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go env GOVERSION: %v", err)
	}
	m := regexp.MustCompile(`go(\d+)\.(\d+)`).FindStringSubmatch(string(out))
	if m == nil {
		t.Fatalf("go env GOVERSION printed %q, which holds no major.minor version", out)
	}
	return m[1] + "." + m[2]
}

// setupScript prepares the work directory of a script and the environment git-gen runs in.
//
// $WORK/home is HOME, and $HOME/src/github.com/github/gitignore is a copy of testdata/gitignore that is not
// a git repository. $WORK/xdg is XDG_CONFIG_HOME and holds a copy of testdata/boilerplate, which a script
// may change. $WORK/gitconfig is the global git configuration with the signing stub testdata/signer/ok.sh;
// $WORK/gitconfig-fail names testdata/signer/fail.sh instead. $WORK/nogo holds only git-gen and cat, so
// PATH=$NOGO_PATH runs git-gen without the go command. $GOLDEN is testdata/golden and $GO_MINOR the go
// line that go.mod gets.
func setupScript(env *testscript.Env, fixtures, goMinor string) error {
	work := env.WorkDir
	home := filepath.Join(work, "home")
	xdg := filepath.Join(work, "xdg")

	if err := copyTree(filepath.Join(fixtures, "gitignore"), filepath.Join(home, "src", "github.com", "github", "gitignore")); err != nil {
		return err
	}
	if err := copyTree(filepath.Join(fixtures, "boilerplate"), filepath.Join(xdg, "boilerplate")); err != nil {
		return err
	}
	for name, signer := range map[string]string{"gitconfig": "ok.sh", "gitconfig-fail": "fail.sh"} {
		if err := os.WriteFile(filepath.Join(work, name), gitConfig(filepath.Join(fixtures, "signer", signer)), 0o644); err != nil {
			return err
		}
	}
	nogo := filepath.Join(work, "nogo")
	if err := linkCommands(nogo, "git-gen", "cat"); err != nil {
		return err
	}

	vars := []struct{ name, value string }{
		{"HOME", home},
		{"XDG_CONFIG_HOME", xdg},
		{"GIT_CONFIG_GLOBAL", filepath.Join(work, "gitconfig")},
		{"GIT_CONFIG_NOSYSTEM", "1"},
		{"GIT_AUTHOR_NAME", ""},
		{"GIT_AUTHOR_EMAIL", ""},
		{"GIT_COMMITTER_NAME", ""},
		{"GIT_COMMITTER_EMAIL", ""},
		{"GH_TOKEN", ""},
		{"GITHUB_TOKEN", ""},
		{"GH_PATH", "/nonexistent"},
		{"GH_CONFIG_DIR", filepath.Join(work, ".gh")},
		{"AUTHOR", "git-gen"},
		{"ORGANIZATION_NAME", ""},
		{"PROJECT_NAME", ""},
		{"GOPATH", filepath.Join(work, "gopath")},
		{"GOCACHE", filepath.Join(work, "gocache")},
		{"GOFLAGS", ""},
		{"GOTOOLCHAIN", "local"},
		{"GOPROXY", "off"},
		{"GOLDEN", filepath.Join(fixtures, "golden")},
		{"NOGO_PATH", nogo},
		{"GO_MINOR", goMinor},
	}
	for _, v := range vars {
		env.Setenv(v.name, v.value)
	}
	return nil
}

// gitConfig returns a global git configuration with a fixed identity and default branch that signs with
// the program at signer.
func gitConfig(signer string) []byte {
	return fmt.Appendf(nil, `[user]
	name = Test Author
	email = author@example.com
[init]
	defaultBranch = trunk
[gpg]
	program = %q
[commit]
	gpgSign = true
`, signer)
}

// copyTree copies the directories, regular files and symbolic links under src to dst. Links are copied as
// links, not followed.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(target, data, info.Mode().Perm())
		}
		return nil
	})
}

// linkCommands creates dir with a symbolic link to each named command as found on PATH.
func linkCommands(dir string, commands ...string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range commands {
		target, err := exec.LookPath(name)
		if err != nil {
			return err
		}
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}

// cmdExitCode implements "exitcode <code> <command> [args...]": it runs the command like exec and fails
// unless the command exits with code. Its output is kept for stdout and stderr.
func cmdExitCode(ts *testscript.TestScript, neg bool, args []string) {
	if neg {
		ts.Fatalf("unsupported: ! exitcode")
	}
	if len(args) < 2 {
		ts.Fatalf("usage: exitcode <code> <command> [args...]")
	}
	want, err := strconv.Atoi(args[0])
	ts.Check(err)

	got := 0
	if err := ts.Exec(args[1], args[2:]...); err != nil {
		exitErr, ok := errors.AsType[*exec.ExitError](err)
		if !ok {
			ts.Fatalf("%s: %v", args[1], err)
		}
		got = exitErr.ExitCode()
	}
	if got != want {
		ts.Fatalf("%s exited with code %d, want %d", args[1], got, want)
	}
}

// cmdEmptyDir implements "emptydir <dir>": it fails unless dir exists and has no entries.
func cmdEmptyDir(ts *testscript.TestScript, neg bool, args []string) {
	if neg {
		ts.Fatalf("unsupported: ! emptydir")
	}
	if len(args) != 1 {
		ts.Fatalf("usage: emptydir <dir>")
	}
	entries, err := os.ReadDir(ts.MkAbs(args[0]))
	ts.Check(err)

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) > 0 {
		ts.Fatalf("%s is not empty: %s", args[0], strings.Join(names, " "))
	}
}

// cmdTree implements "tree <dir>": it prints every entry under dir except .git, one slash-separated path
// per line in byte order, with a trailing slash on directories.
func cmdTree(ts *testscript.TestScript, neg bool, args []string) {
	if neg {
		ts.Fatalf("unsupported: ! tree")
	}
	if len(args) != 1 {
		ts.Fatalf("usage: tree <dir>")
	}
	root := ts.MkAbs(args[0])

	var paths []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == root {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return fs.SkipDir
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			rel += "/"
		}
		paths = append(paths, rel)
		return nil
	})
	ts.Check(err)

	slices.Sort(paths)
	for _, p := range paths {
		fmt.Fprintln(ts.Stdout(), p)
	}
}

func TestParseArgs(t *testing.T) {
	apache, ok := license.Lookup("apache2")
	if !ok {
		t.Fatal(`license.Lookup("apache2") found nothing`)
	}
	mit, ok := license.Lookup("mit-owner")
	if !ok {
		t.Fatal(`license.Lookup("mit-owner") found nothing`)
	}

	tests := map[string]struct {
		args      []string
		want      options
		wantHelp  bool
		wantUsage string
	}{
		"success: license and one language": {
			args: []string{"apache2", "go"},
			want: options{license: apache, langs: []string{"go"}},
		},
		"success: verbose with several languages": {
			args: []string{"-v", "mit-owner", "Go", "Rust"},
			want: options{verbose: true, license: mit, langs: []string{"Go", "Rust"}},
		},
		"success: arguments after the flag terminator": {
			args: []string{"--", "apache2", "go"},
			want: options{license: apache, langs: []string{"go"}},
		},
		"success: list": {
			args: []string{"-l"},
			want: options{list: true},
		},
		"success: list with debug logging": {
			args: []string{"-v", "-l"},
			want: options{list: true, verbose: true},
		},
		"error: help": {
			args:     []string{"-h"},
			wantHelp: true,
		},
		"error: no arguments": {
			args:      nil,
			wantUsage: "want a license and at least one language",
		},
		"error: license without a language": {
			args:      []string{"apache2"},
			wantUsage: "want a license and at least one language",
		},
		// The argument count is checked before the license, so a lone language is not an unknown license.
		"error: a language alone": {
			args:      []string{"Go"},
			wantUsage: "want a license and at least one language",
		},
		"error: flag after the arguments": {
			args:      []string{"apache2", "go", "-v"},
			wantUsage: `argument "-v" starts with '-': flags come before the arguments`,
		},
		"error: dash as a language": {
			args:      []string{"apache2", "-"},
			wantUsage: `argument "-" starts with '-': flags come before the arguments`,
		},
		"error: dash argument after the flag terminator": {
			args:      []string{"--", "-l"},
			wantUsage: `argument "-l" starts with '-': flags come before the arguments`,
		},
		"error: list with an argument": {
			args:      []string{"-l", "Go"},
			wantUsage: "-l takes no arguments",
		},
		"error: unknown license": {
			args:      []string{"gpl", "go"},
			wantUsage: `unknown license "gpl"`,
		},
		"error: license names are case-sensitive": {
			args:      []string{"MIT", "go"},
			wantUsage: `unknown license "MIT"`,
		},
		"error: unknown flag": {
			args:      []string{"-x", "apache2", "go"},
			wantUsage: "flag provided but not defined: -x",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := parseArgs(tt.args)
			switch {
			case tt.wantHelp:
				if !errors.Is(err, flag.ErrHelp) {
					t.Fatalf("parseArgs(%q) error = %v, want flag.ErrHelp", tt.args, err)
				}
				return
			case tt.wantUsage != "":
				usageErr, ok := errors.AsType[*usageError](err)
				if !ok {
					t.Fatalf("parseArgs(%q) error = %v, want a *usageError", tt.args, err)
				}
				if diff := gocmp.Diff(tt.wantUsage, usageErr.Error()); diff != "" {
					t.Errorf("parseArgs(%q) usage error mismatch (-want +got):\n%s", tt.args, diff)
				}
				return
			case err != nil:
				t.Fatalf("parseArgs(%q) error = %v", tt.args, err)
			}
			if diff := gocmp.Diff(tt.want, got, gocmp.AllowUnexported(options{})); diff != "" {
				t.Errorf("parseArgs(%q) mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

func TestResolveNames(t *testing.T) {
	tests := map[string]struct {
		env       map[string]string
		wd        string
		want      names
		wantUsage string
	}{
		"success: defaults from the working directory": {
			wd:   "/src/github.com/acme/rocket",
			want: names{org: "acme", project: "rocket", author: "rocket"},
		},
		"success: environment overrides every default": {
			env:  map[string]string{"ORGANIZATION_NAME": "org", "PROJECT_NAME": "proj", "AUTHOR": "The Team"},
			wd:   "/src/github.com/acme/rocket",
			want: names{org: "org", project: "proj", author: "The Team"},
		},
		"success: author defaults to PROJECT_NAME": {
			env:  map[string]string{"PROJECT_NAME": "proj"},
			wd:   "/src/github.com/acme/rocket",
			want: names{org: "acme", project: "proj", author: "proj"},
		},
		"success: empty values count as unset": {
			env:  map[string]string{"ORGANIZATION_NAME": "", "PROJECT_NAME": "", "AUTHOR": ""},
			wd:   "/tmp/foo",
			want: names{org: "tmp", project: "foo", author: "foo"},
		},
		"success: GitHub's name characters and an author with spaces and non-ASCII letters": {
			env:  map[string]string{"ORGANIZATION_NAME": "my-org_2", "PROJECT_NAME": "go.v2", "AUTHOR": "Jürgen Müller & Co."},
			wd:   "/src/x/y",
			want: names{org: "my-org_2", project: "go.v2", author: "Jürgen Müller & Co."},
		},
		"error: ORGANIZATION_NAME with a newline": {
			env:       map[string]string{"ORGANIZATION_NAME": "org\n  evil: true"},
			wd:        "/src/acme/rocket",
			wantUsage: `invalid ORGANIZATION_NAME "org\n  evil: true": want letters, digits, '.', '-' and '_', and not "." or ".."`,
		},
		"error: PROJECT_NAME with a slash": {
			env:       map[string]string{"PROJECT_NAME": "a/b"},
			wd:        "/src/acme/rocket",
			wantUsage: `invalid PROJECT_NAME "a/b": want letters, digits, '.', '-' and '_', and not "." or ".."`,
		},
		"error: PROJECT_NAME is ..": {
			env:       map[string]string{"PROJECT_NAME": ".."},
			wd:        "/src/acme/rocket",
			wantUsage: `invalid PROJECT_NAME "..": want letters, digits, '.', '-' and '_', and not "." or ".."`,
		},
		"error: PROJECT_NAME is .": {
			env:       map[string]string{"PROJECT_NAME": "."},
			wd:        "/src/acme/rocket",
			wantUsage: `invalid PROJECT_NAME ".": want letters, digits, '.', '-' and '_', and not "." or ".."`,
		},
		"error: AUTHOR with a control character": {
			env:       map[string]string{"AUTHOR": "The Team\n* filter=lfs"},
			wd:        "/src/acme/rocket",
			wantUsage: `invalid AUTHOR "The Team\n* filter=lfs": it contains a control character`,
		},
		"error: a project directory whose name has a space": {
			wd:        "/src/acme/my rocket",
			wantUsage: `invalid project name "my rocket" taken from the directory "/src/acme/my rocket": set PROJECT_NAME; want letters, digits, '.', '-' and '_', and not "." or ".."`,
		},
		"error: an organization directory whose name has a newline": {
			wd:        "/src/ac\nme/rocket",
			wantUsage: `invalid organization name "ac\nme" taken from the directory "/src/ac\nme": set ORGANIZATION_NAME; want letters, digits, '.', '-' and '_', and not "." or ".."`,
		},
		"error: the root directory has no organization name": {
			wd:        "/rocket",
			wantUsage: `invalid organization name "/" taken from the directory "/": set ORGANIZATION_NAME; want letters, digits, '.', '-' and '_', and not "." or ".."`,
		},
		"success: variables replace invalid directory names": {
			env:  map[string]string{"ORGANIZATION_NAME": "acme", "PROJECT_NAME": "rocket"},
			wd:   "/src/ac me/my rocket",
			want: names{org: "acme", project: "rocket", author: "rocket"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			getenv := func(key string) string { return tt.env[key] }
			got, err := resolveNames(getenv, filepath.FromSlash(tt.wd))
			if tt.wantUsage != "" {
				usageErr, ok := errors.AsType[*usageError](err)
				if !ok {
					t.Fatalf("resolveNames(%q) error = %v, want a *usageError", tt.wd, err)
				}
				if diff := gocmp.Diff(filepath.FromSlash(tt.wantUsage), usageErr.Error()); diff != "" {
					t.Errorf("resolveNames(%q) usage error mismatch (-want +got):\n%s", tt.wd, diff)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveNames(%q) error = %v", tt.wd, err)
			}
			if diff := gocmp.Diff(tt.want, got, gocmp.AllowUnexported(names{})); diff != "" {
				t.Errorf("resolveNames(%q) mismatch (-want +got):\n%s", tt.wd, diff)
			}
		})
	}
}
