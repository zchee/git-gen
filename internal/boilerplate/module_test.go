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

package boilerplate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

// fakeGoEnv makes the test binary act as the go command, so that the paths the
// real toolchain cannot reach (an unparsable version, failing subcommands) run
// through InitModule unchanged.
const fakeGoEnv = "BOILERPLATE_TEST_FAKE_GO"

func TestMain(m *testing.M) {
	if os.Getenv(fakeGoEnv) == "1" {
		os.Exit(fakeGo(os.Args[1:]))
	}
	m.Run()
}

// fakeGo stands in for the go command. FAKE_GO_VERSION is what
// `go env GOVERSION` prints, and every command that starts with FAKE_GO_FAIL,
// such as "mod edit", exits 1.
func fakeGo(args []string) int {
	command := strings.Join(args, " ")
	if fail := os.Getenv("FAKE_GO_FAIL"); fail != "" && strings.HasPrefix(command, fail) {
		fmt.Fprintf(os.Stderr, "fake go: %s: failed on purpose\n", command)
		return 1
	}

	switch {
	case command == "env GOVERSION":
		fmt.Fprintln(os.Stdout, os.Getenv("FAKE_GO_VERSION"))
	case command == "mod init":
		if err := os.WriteFile("go.mod", []byte("module fake.example/inferred\n"), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	case len(args) == 3 && args[0] == "mod" && args[1] == "edit" && strings.HasPrefix(args[2], "-go="):
		f, err := os.OpenFile("go.mod", os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		_, err = fmt.Fprintf(f, "\ngo %s\n", strings.TrimPrefix(args[2], "-go="))
		if err := errors.Join(err, f.Close()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	default:
		fmt.Fprintf(os.Stderr, "fake go: unexpected command %q\n", command)
		return 2
	}

	return 0
}

// goEnv returns an environment for the real go command whose state lives under
// a temporary directory, and the GOPATH it sets. GOTOOLCHAIN and GOPROXY keep
// the go command off the network.
func goEnv(t *testing.T) ([]string, string) {
	t.Helper()

	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the real go command is required: %v", err)
	}
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	gopath := filepath.Join(tmp, "gopath")

	return []string{
		"PATH=" + filepath.Dir(gobin),
		"HOME=" + home,
		"GOPATH=" + gopath,
		"GOCACHE=" + filepath.Join(tmp, "gocache"),
		"GOMODCACHE=" + filepath.Join(tmp, "gomodcache"),
		"GOFLAGS=",
		"GOTOOLCHAIN=local",
		"GOPROXY=off",
	}, gopath
}

// realGoVersion returns what `go env GOVERSION` prints in env.
func realGoVersion(t *testing.T, env []string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "go", "env", "GOVERSION")
	cmd.Env = env
	cmd.Dir = t.TempDir()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go env GOVERSION: %v", err)
	}

	return strings.TrimSpace(string(out))
}

// TestInitModule checks the module path, the go line and go.sum with the real go command.
func TestInitModule(t *testing.T) {
	t.Parallel()

	const fallback = "github.com/acme/widget"

	tests := map[string]struct {
		root       func(tmp, gopath string) string
		gomod      string // written before InitModule when not empty
		gosum      string // written before InitModule when not empty
		fallback   string
		wantModule string
		wantGoSum  string
		wantErr    bool
	}{
		"success: inside GOPATH the module path is inferred": {
			root:       func(_, gopath string) string { return filepath.Join(gopath, "src", "example.com", "foo", "bar") },
			fallback:   fallback,
			wantModule: "example.com/foo/bar",
		},
		"success: outside GOPATH the fallback path is used": {
			root:       func(tmp, _ string) string { return filepath.Join(tmp, "work", "widget") },
			fallback:   fallback,
			wantModule: fallback,
		},
		"success: an existing go.sum is kept": {
			root:       func(tmp, _ string) string { return filepath.Join(tmp, "work", "widget") },
			gosum:      "example.com/dep v1.0.0 h1:AAAA=\n",
			fallback:   fallback,
			wantModule: fallback,
			wantGoSum:  "example.com/dep v1.0.0 h1:AAAA=\n",
		},
		"success: an existing go.mod is not changed": {
			root:     func(tmp, _ string) string { return filepath.Join(tmp, "work", "widget") },
			gomod:    "module keep.example/me\n\ngo 1.20\n",
			fallback: fallback,
		},
		"error: outside GOPATH with a malformed fallback path": {
			root:     func(tmp, _ string) string { return filepath.Join(tmp, "work", "widget") },
			fallback: "not a module path",
			wantErr:  true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env, gopath := goEnv(t)
			root := tt.root(t.TempDir(), gopath)
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			if tt.gomod != "" {
				writeFile(t, root, "go.mod", tt.gomod)
			}
			if tt.gosum != "" {
				writeFile(t, root, "go.sum", tt.gosum)
			}

			got, err := InitModule(t.Context(), ModuleOptions{Root: root, FallbackPath: tt.fallback, Env: env})
			if (err != nil) != tt.wantErr {
				t.Fatalf("InitModule() error = %v, wantErr %t", err, tt.wantErr)
			}
			if got.Warning != "" {
				t.Errorf("InitModule() Warning = %q, want none", got.Warning)
			}

			switch {
			case tt.wantErr:
				if _, err := os.Lstat(filepath.Join(root, "go.mod")); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("go.mod after a failed init: Lstat error = %v, want fs.ErrNotExist", err)
				}
			case tt.gomod != "":
				if diff := gocmp.Diff(ModuleResult{}, got); diff != "" {
					t.Errorf("InitModule() mismatch (-want +got):\n%s", diff)
				}
				if diff := gocmp.Diff(tt.gomod, readFile(t, root, "go.mod")); diff != "" {
					t.Errorf("go.mod changed (-want +got):\n%s", diff)
				}
				if _, err := os.Lstat(filepath.Join(root, "go.sum")); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("go.sum next to an existing go.mod: Lstat error = %v, want fs.ErrNotExist", err)
				}
			default:
				checkCreated(t, env, root, got, tt.wantModule, tt.wantGoSum)
			}
		})
	}
}

// checkCreated checks a created module against the toolchain that env runs:
// the go line is the first major.minor of `go env GOVERSION`.
func checkCreated(t *testing.T, env []string, root string, got ModuleResult, wantModule, wantGoSum string) {
	t.Helper()

	if !got.Created || got.ModulePath != wantModule {
		t.Errorf("InitModule() = %+v, want Created with ModulePath %q", got, wantModule)
	}
	goversion := realGoVersion(t, env)
	if !regexp.MustCompile(`^\d+\.\d+$`).MatchString(got.GoVersion) {
		t.Fatalf("GoVersion = %q, want major.minor", got.GoVersion)
	}
	if rest, ok := strings.CutPrefix(goversion[strings.Index(goversion, "go")+2:], got.GoVersion); !ok || (rest != "" && rest[0] >= '0' && rest[0] <= '9') {
		t.Errorf("GoVersion = %q, want the first major.minor of %q", got.GoVersion, goversion)
	}

	lines := strings.Split(readFile(t, root, "go.mod"), "\n")
	for _, want := range []string{"module " + wantModule, "go " + got.GoVersion} {
		if !slices.Contains(lines, want) {
			t.Errorf("go.mod lines = %q, want the line %q", lines, want)
		}
	}
	if diff := gocmp.Diff(wantGoSum, readFile(t, root, "go.sum")); diff != "" {
		t.Errorf("go.sum mismatch (-want +got):\n%s", diff)
	}
}

// TestInitModuleGoVersion feeds the GOVERSION forms of the go command to
// InitModule through the fake go command.
func TestInitModuleGoVersion(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		goversion   string
		wantVersion string
	}{
		"success: release":                  {goversion: "go1.27.1", wantVersion: "1.27"},
		"success: release before Go 1.10":   {goversion: "go1.9.2", wantVersion: "1.9"},
		"success: release candidate":        {goversion: "go1.28rc1", wantVersion: "1.28"},
		"success: devel prefix":             {goversion: "devel go1.28-abcdef", wantVersion: "1.28"},
		"success: devel suffix with a date": {goversion: "go1.28-devel_fe515272d0 Fri Sep 25 18:46:22 2026 +0900 X:loopvar", wantVersion: "1.28"},
		"success: no minor version warns":   {goversion: "go1"},
		"success: empty output warns":       {goversion: ""},
		"success: not a version warns":      {goversion: "gopher"},
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			got, err := InitModule(t.Context(), ModuleOptions{
				Root:         root,
				FallbackPath: "github.com/acme/widget",
				Env:          []string{fakeGoEnv + "=1", "FAKE_GO_VERSION=" + tt.goversion},
				LookPath:     func(string) (string, error) { return self, nil },
			})
			if err != nil {
				t.Fatalf("InitModule() error = %v", err)
			}

			if tt.wantVersion == "" {
				if got.Created || !strings.Contains(got.Warning, "go.mod not created") || !strings.Contains(got.Warning, fmt.Sprintf("%q", tt.goversion)) {
					t.Errorf("InitModule() = %+v, want a warning that quotes %q", got, tt.goversion)
				}
				if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
					t.Errorf("root after a warning holds %v (error %v), want nothing", entries, err)
				}
				return
			}

			want := ModuleResult{Created: true, ModulePath: "fake.example/inferred", GoVersion: tt.wantVersion}
			if diff := gocmp.Diff(want, got); diff != "" {
				t.Errorf("InitModule() mismatch (-want +got):\n%s", diff)
			}
			if diff := gocmp.Diff("module fake.example/inferred\n\ngo "+tt.wantVersion+"\n", readFile(t, root, "go.mod")); diff != "" {
				t.Errorf("go.mod mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestInitModuleFailures covers the go command being absent or failing.
func TestInitModuleFailures(t *testing.T) {
	t.Parallel()

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fake := func(string) (string, error) { return self, nil }

	tests := map[string]struct {
		lookPath    func(string) (string, error)
		fail        string
		cancel      bool
		wantResult  ModuleResult
		wantWarning string
		wantErr     bool
		wantErrIs   error
		wantGoMod   bool
	}{
		"success: a missing go command warns": {
			lookPath:    func(file string) (string, error) { return "", &exec.Error{Name: file, Err: exec.ErrNotFound} },
			wantWarning: "executable file not found",
		},
		"success: a failing go env warns": {
			lookPath:    fake,
			fail:        "env",
			wantWarning: "failed on purpose",
		},
		"error: both go mod init commands fail": {
			lookPath: fake,
			fail:     "mod init",
			wantErr:  true,
		},
		"error: go mod edit fails": {
			lookPath:   fake,
			fail:       "mod edit",
			wantResult: ModuleResult{Created: true, GoVersion: "1.27"},
			wantErr:    true,
			wantGoMod:  true,
		},
		"error: canceled context": {
			lookPath:  fake,
			cancel:    true,
			wantErr:   true,
			wantErrIs: context.Canceled,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.cancel {
				cancel()
			}

			root := t.TempDir()
			got, err := InitModule(ctx, ModuleOptions{
				Root:         root,
				FallbackPath: "github.com/acme/widget",
				Env:          []string{fakeGoEnv + "=1", "FAKE_GO_VERSION=go1.27.1", "FAKE_GO_FAIL=" + tt.fail},
				LookPath:     tt.lookPath,
			})

			if (err != nil) != tt.wantErr {
				t.Fatalf("InitModule() error = %v, wantErr %t", err, tt.wantErr)
			}
			if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
				t.Errorf("InitModule() error = %v, want errors.Is %v", err, tt.wantErrIs)
			}
			if tt.wantWarning != "" {
				if got.Created || !strings.Contains(got.Warning, "go.mod not created") || !strings.Contains(got.Warning, tt.wantWarning) {
					t.Errorf("InitModule() = %+v, want a warning containing %q", got, tt.wantWarning)
				}
			} else if diff := gocmp.Diff(tt.wantResult, got); diff != "" {
				t.Errorf("InitModule() mismatch (-want +got):\n%s", diff)
			}

			_, statErr := os.Lstat(filepath.Join(root, "go.mod"))
			if gotGoMod := statErr == nil; gotGoMod != tt.wantGoMod {
				t.Errorf("go.mod exists = %t, want %t", gotGoMod, tt.wantGoMod)
			}
			if _, err := os.Lstat(filepath.Join(root, "go.sum")); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("go.sum after a failure: Lstat error = %v, want fs.ErrNotExist", err)
			}
		})
	}
}

func TestInitModuleRootNotDirectory(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "file")
	writeFile(t, filepath.Dir(root), "file", "not a directory\n")

	got, err := InitModule(t.Context(), ModuleOptions{
		Root:     root,
		LookPath: func(file string) (string, error) { return "", &exec.Error{Name: file, Err: exec.ErrNotFound} },
	})
	if err == nil {
		t.Fatalf("InitModule() = %+v, nil; want an error", got)
	}
	if diff := gocmp.Diff(ModuleResult{}, got); diff != "" {
		t.Errorf("InitModule() mismatch (-want +got):\n%s", diff)
	}
}

func TestModulePath(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		gomod string
		want  string
	}{
		"success: plain":               {gomod: "module example.com/foo/bar\n\ngo 1.27\n", want: "example.com/foo/bar"},
		"success: quoted":              {gomod: "module \"example.com/foo bar\"\n", want: "example.com/foo bar"},
		"success: trailing comment":    {gomod: "// header\nmodule example.com/x // Deprecated: use y\n", want: "example.com/x"},
		"success: tab separator":       {gomod: "module\texample.com/tab\n", want: "example.com/tab"},
		"error: no module directive":   {gomod: "go 1.27\n", want: ""},
		"error: similar directive":     {gomod: "modules example.com/no\n", want: ""},
		"error: directive without arg": {gomod: "module\n", want: ""},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if diff := gocmp.Diff(tt.want, modulePath([]byte(tt.gomod))); diff != "" {
				t.Errorf("modulePath() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
