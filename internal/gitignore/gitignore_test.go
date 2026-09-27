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

package gitignore

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

var (
	// fixtureDir holds templates copied from github/gitignore at commit b06d69d
	// and the symbolic link Alias.gitignore.
	fixtureDir = filepath.Join("..", "..", "testdata", "gitignore")
	// escapeFile lies one level above fixtureDir. No output may contain its
	// sentinel line.
	escapeFile = filepath.Join("..", "..", "testdata", "escape.gitignore")
)

// fixtureNames is the listing of fixtureDir.
var fixtureNames = []string{"Global/JetBrains", "Go", "Python", "Rust", "community/Golang"}

func openCatalog(tb testing.TB, dir string) *Catalog {
	tb.Helper()
	c, err := Open(dir)
	if err != nil {
		tb.Fatalf("Open(%q): %v", dir, err)
	}
	tb.Cleanup(func() {
		if err := c.Close(); err != nil {
			tb.Errorf("Close: %v", err)
		}
	})
	return c
}

func readFile(tb testing.TB, name string) []byte {
	tb.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

// writeTree creates files under dir. A name that ends with "/" creates a
// directory, and a content that starts with "-> " creates a symbolic link to
// the rest of the content.
func writeTree(tb testing.TB, dir string, files map[string]string) string {
	tb.Helper()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			tb.Fatal(err)
		}
		switch target, isLink := strings.CutPrefix(content, "-> "); {
		case strings.HasSuffix(name, "/"):
			if err := os.MkdirAll(p, 0o755); err != nil {
				tb.Fatal(err)
			}
		case isLink:
			if err := os.Symlink(target, p); err != nil {
				tb.Fatal(err)
			}
		default:
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				tb.Fatal(err)
			}
		}
	}
	return dir
}

// copyFixture copies fixtureDir to <temp>/<sub>/gitignore, keeping the
// symbolic link, and returns the copy.
func copyFixture(t *testing.T, sub string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), filepath.FromSlash(sub), "gitignore")
	if err := os.CopyFS(dst, os.DirFS(fixtureDir)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(dst, "Alias.gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		t.Fatalf("copied Alias.gitignore has mode %v, want a symbolic link", info.Mode())
	}
	return dst
}

func TestOpen(t *testing.T) {
	tests := map[string]struct {
		dir          string
		wantErr      bool
		wantNotExist bool
	}{
		"success: fixture": {
			dir: fixtureDir,
		},
		"error: missing directory": {
			dir:          filepath.Join(t.TempDir(), "missing"),
			wantErr:      true,
			wantNotExist: true,
		},
		"error: regular file": {
			dir:     escapeFile,
			wantErr: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c, err := Open(tt.dir)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Open(%q) error = %v, wantErr %v", tt.dir, err, tt.wantErr)
			}
			if err != nil {
				if got := errors.Is(err, fs.ErrNotExist); got != tt.wantNotExist {
					t.Errorf("errors.Is(%v, fs.ErrNotExist) = %v, want %v", err, got, tt.wantNotExist)
				}
				return
			}
			if err := c.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		})
	}
}

func TestList(t *testing.T) {
	tests := map[string]struct {
		dir  string
		want []string
	}{
		"success: fixture": {
			dir:  fixtureDir,
			want: fixtureNames,
		},
		"success: fixture copied one level below a temporary directory": {
			dir:  copyFixture(t, ""),
			want: fixtureNames,
		},
		"success: fixture copied six levels below a temporary directory": {
			dir:  copyFixture(t, "a/b/c/d/e"),
			want: fixtureNames,
		},
		"success: depth limit, cut to two elements, duplicates and file types": {
			dir: writeTree(t, t.TempDir(), map[string]string{
				"Top.gitignore":          "top\n",
				"a/Two.gitignore":        "two\n",
				"a/b/Three.gitignore":    "three\n",
				"a/b/Other.gitignore":    "other\n",
				"a/b/c/Four.gitignore":   "four levels deep\n",
				".hidden/H.gitignore":    "hidden directory\n",
				".gitignore":             "checkout ignore file\n",
				"a/.gitignore":           "nested ignore file\n",
				"README.md":              "not a template\n",
				"Dir.gitignore/":         "",
				"Link.gitignore":         "-> Top.gitignore",
				"Dangling.gitignore":     "-> missing.gitignore",
				"LinkDir/":               "",
				"LinkDir/Kept.gitignore": "kept\n",
				"Linked":                 "-> LinkDir",
			}),
			want: []string{".hidden/H", "LinkDir/Kept", "Top", "a/Two", "a/b"},
		},
		"success: empty catalog": {
			dir:  t.TempDir(),
			want: nil,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := openCatalog(t, tt.dir)
			got, err := c.List()
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if diff := gocmp.Diff(tt.want, got); diff != "" {
				t.Errorf("List mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	const goScaffold = ScaffoldGo | ScaffoldGoSumAttributes
	tests := map[string]struct {
		dir         string
		args        []string
		wantLangs   []Language
		wantUnknown []string
	}{
		"success: language table": {
			dir:  fixtureDir,
			args: []string{"go", "Go", "go-pkg", "go-simple", "Rust", "community/Golang"},
			wantLangs: []Language{
				{Arg: "go", Template: "Go", Scaffold: goScaffold},
				{Arg: "Go", Template: "Go", Scaffold: goScaffold | ScaffoldMakefile},
				{Arg: "go-pkg", Template: "Go"},
				{Arg: "go-simple", Template: "Go"},
				{Arg: "Rust", Template: "Rust"},
				{Arg: "community/Golang", Template: "community/Golang", Scaffold: ScaffoldGo | ScaffoldMakefile},
			},
		},
		"success: names outside the table resolve by file name": {
			dir:  fixtureDir,
			args: []string{"Python", "Global/JetBrains", "Alias"},
			wantLangs: []Language{
				{Arg: "Python", Template: "Python"},
				{Arg: "Global/JetBrains", Template: "Global/JetBrains"},
				{Arg: "Alias", Template: "Alias"},
			},
		},
		"success: unknown names, including other cases of existing names": {
			dir: fixtureDir,
			args: []string{
				"go-old", "python", "GO", "rust", "community/golang", "Global", "community",
				"Nope", "Go.gitignore", "./Go", "Go/", "community//Golang", "community/Golang/", ".",
			},
			wantUnknown: []string{
				"go-old", "python", "GO", "rust", "community/golang", "Global", "community",
				"Nope", "Go.gitignore", "./Go", "Go/", "community//Golang", "community/Golang/", ".",
			},
		},
		"success: order is kept": {
			dir:         fixtureDir,
			args:        []string{"Nope", "Python", "go", "go-old", "go"},
			wantLangs:   []Language{{Arg: "Python", Template: "Python"}, {Arg: "go", Template: "Go", Scaffold: goScaffold}, {Arg: "go", Template: "Go", Scaffold: goScaffold}},
			wantUnknown: []string{"Nope", "go-old"},
		},
		"success: no arguments": {
			dir: fixtureDir,
		},
		"success: allowlist of a directory outside the table": {
			dir: writeTree(t, t.TempDir(), map[string]string{
				"Foo/Foo.AllowList.gitignore": "*\n",
				"Foo/Other.gitignore":         "other\n",
			}),
			args:      []string{"Foo"},
			wantLangs: []Language{{Arg: "Foo", Template: "Foo"}},
		},
		"success: directory without an allowlist is unknown": {
			dir:         writeTree(t, t.TempDir(), map[string]string{"Bar/Bar.gitignore": "bar\n", "Bar/Dir.AllowList.gitignore/": ""}),
			args:        []string{"Bar"},
			wantUnknown: []string{"Bar"},
		},
		"success: template file wins over a directory of the same name": {
			dir: writeTree(t, t.TempDir(), map[string]string{
				"Baz.gitignore":               "baz\n",
				"Baz/Baz.AllowList.gitignore": "*\n",
			}),
			args:      []string{"Baz"},
			wantLangs: []Language{{Arg: "Baz", Template: "Baz"}},
		},
		"success: table entry whose template is missing is unknown": {
			dir:         writeTree(t, t.TempDir(), map[string]string{"go.gitignore": "lower case\n", "community/Golang/Go.gitignore": "no allowlist\n"}),
			args:        []string{"go", "Rust", "community/Golang"},
			wantUnknown: []string{"go", "Rust", "community/Golang"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := openCatalog(t, tt.dir)
			langs, unknown, err := c.Resolve(tt.args)
			if err != nil {
				t.Fatalf("Resolve(%q): %v", tt.args, err)
			}
			if diff := gocmp.Diff(tt.wantLangs, langs); diff != "" {
				t.Errorf("Resolve(%q) langs mismatch (-want +got):\n%s", tt.args, diff)
			}
			if diff := gocmp.Diff(tt.wantUnknown, unknown); diff != "" {
				t.Errorf("Resolve(%q) unknown mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

func TestResolveError(t *testing.T) {
	ambiguous := writeTree(t, t.TempDir(), map[string]string{
		"Two/A.AllowList.gitignore": "a\n",
		"Two/B.AllowList.gitignore": "b\n",
	})
	tests := map[string]struct {
		dir         string
		args        []string
		wantInvalid string // name in the *InvalidNameError; empty means another error
		wantText    string
	}{
		"error: empty name": {
			dir:         fixtureDir,
			args:        []string{""},
			wantInvalid: "",
			wantText:    `invalid template name ""`,
		},
		"error: absolute name": {
			dir:         fixtureDir,
			args:        []string{"/etc/passwd"},
			wantInvalid: "/etc/passwd",
			wantText:    `invalid template name "/etc/passwd"`,
		},
		"error: parent directory": {
			dir:         fixtureDir,
			args:        []string{".."},
			wantInvalid: "..",
			wantText:    `invalid template name ".."`,
		},
		"error: dot-dot element that stays inside lexically": {
			dir:         fixtureDir,
			args:        []string{"community/../Go"},
			wantInvalid: "community/../Go",
			wantText:    `invalid template name "community/../Go"`,
		},
		"error: invalid name after valid ones": {
			dir:         fixtureDir,
			args:        []string{"go", "Nope", "../escape"},
			wantInvalid: "../escape",
			wantText:    `invalid template name "../escape"`,
		},
		"error: directory with two allowlist templates": {
			dir:      ambiguous,
			args:     []string{"Two"},
			wantText: "Two holds 2 allowlist templates (Two/A.AllowList.gitignore, Two/B.AllowList.gitignore); want one",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := openCatalog(t, tt.dir)
			langs, unknown, err := c.Resolve(tt.args)
			if err == nil {
				t.Fatalf("Resolve(%q) = %v, %v, nil; want an error", tt.args, langs, unknown)
			}
			if langs != nil || unknown != nil {
				t.Errorf("Resolve(%q) = %v, %v with error; want nil, nil", tt.args, langs, unknown)
			}
			if !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("Resolve(%q) error = %q, want it to contain %q", tt.args, err, tt.wantText)
			}
			var invalid *InvalidNameError
			isInvalid := errors.As(err, &invalid)
			wantIsInvalid := strings.Contains(tt.wantText, "invalid template name")
			if isInvalid != wantIsInvalid {
				t.Fatalf("errors.As(%v, *InvalidNameError) = %v, want %v", err, isInvalid, wantIsInvalid)
			}
			if isInvalid && invalid.Name != tt.wantInvalid {
				t.Errorf("InvalidNameError.Name = %q, want %q", invalid.Name, tt.wantInvalid)
			}
		})
	}
}

// TestContainment checks that no read leaves the catalog, through a ".."
// element or through a symbolic link.
func TestContainment(t *testing.T) {
	escape := readFile(t, escapeFile)
	_, sentinel, ok := bytes.Cut(escape, []byte("\n"))
	sentinel = bytes.TrimSpace(sentinel)
	if !ok || !bytes.HasPrefix(sentinel, []byte("ESCAPE-SENTINEL-")) {
		t.Fatalf("%s: second line %q is not the sentinel", escapeFile, sentinel)
	}
	// Reading the name by plain path concatenation, as the script did, reaches the sentinel.
	if naive := readFile(t, filepath.Join(fixtureDir, "../escape"+".gitignore")); !bytes.Contains(naive, sentinel) {
		t.Fatalf("fixture layout: ../escape.gitignore next to %s does not hold the sentinel", fixtureDir)
	}

	base := t.TempDir()
	writeTree(t, base, map[string]string{
		"escape.gitignore":                string(escape),
		"cat/Go.gitignore":                "line 1\nline 2\n*.exe\n",
		"cat/Esc.gitignore":               "-> ../escape.gitignore",
		"cat/Dangling.gitignore":          "-> missing.gitignore",
		"cat/EscDir":                      "-> ..",
		"cat/Out/":                        "",
		"cat/Out/Out.AllowList.gitignore": "-> ../../escape.gitignore",
	})
	linked := filepath.Join(base, "cat")

	tests := map[string]struct {
		dir         string
		arg         string
		wantErr     bool
		wantUnknown bool
	}{
		"error: dot-dot name in the fixture": {
			dir:     fixtureDir,
			arg:     "../escape",
			wantErr: true,
		},
		"error: template symlink leaving the catalog": {
			dir:     linked,
			arg:     "Esc",
			wantErr: true,
		},
		"error: allowlist symlink leaving the catalog": {
			dir:     linked,
			arg:     "Out",
			wantErr: true,
		},
		"success: symlinked directory is not traversed": {
			dir:         linked,
			arg:         "EscDir/escape",
			wantUnknown: true,
		},
		"success: dangling symlink is unknown": {
			dir:         linked,
			arg:         "Dangling",
			wantUnknown: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := openCatalog(t, tt.dir)
			var outputs [][]byte

			langs, unknown, err := c.Resolve([]string{tt.arg})
			if (err != nil) != tt.wantErr {
				t.Fatalf("Resolve(%q) error = %v, wantErr %v", tt.arg, err, tt.wantErr)
			}
			if err != nil {
				outputs = append(outputs, []byte(err.Error()))
			}
			if got := len(unknown) == 1; got != tt.wantUnknown {
				t.Errorf("Resolve(%q) unknown = %q, want unknown %v", tt.arg, unknown, tt.wantUnknown)
			}
			if len(langs) != 0 {
				t.Errorf("Resolve(%q) langs = %v, want none", tt.arg, langs)
			}

			content, warnings, err := c.Compose("git-gen", []Language{{Arg: tt.arg, Template: tt.arg}})
			if err == nil {
				t.Errorf("Compose(%q) = %q, want an error", tt.arg, content)
			} else {
				outputs = append(outputs, []byte(err.Error()))
			}
			if content != nil || warnings != nil {
				t.Errorf("Compose(%q) = %q, %q with error; want nil, nil", tt.arg, content, warnings)
			}

			names, err := c.List()
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			outputs = append(outputs, []byte(strings.Join(names, "\n")))

			for _, out := range outputs {
				if bytes.Contains(out, sentinel) {
					t.Errorf("output %q contains the sentinel line from outside the catalog", out)
				}
			}
		})
	}
}

func TestValidName(t *testing.T) {
	tests := map[string]struct {
		name string
		want bool
	}{
		"success: single element":        {name: "Go", want: true},
		"success: nested":                {name: "community/Golang", want: true},
		"success: dot element":           {name: "./Go", want: true},
		"error: empty":                   {name: ""},
		"error: absolute":                {name: "/Go"},
		"error: leading dot-dot":         {name: "../Go"},
		"error: inner dot-dot":           {name: "a/../Go"},
		"error: trailing dot-dot":        {name: "Go/.."},
		"error: dot-dot only":            {name: ".."},
		"success: dot-dot inside a name": {name: "Go..old", want: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := validName(tt.name); got != tt.want {
				t.Errorf("validName(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}
