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
	"path/filepath"
	"slices"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

// goldenIgnore is the .gitignore that the script wrote for "apache2 Go" with
// author "git-gen" (this repository at commit d8f4bcf).
const goldenIgnore = "testdata/golden/gitignore.Go"

func resolve(tb testing.TB, c *Catalog, args ...string) []Language {
	tb.Helper()
	langs, unknown, err := c.Resolve(args)
	if err != nil {
		tb.Fatalf("Resolve(%q): %v", args, err)
	}
	if len(unknown) != 0 {
		tb.Fatalf("Resolve(%q): unknown %q", args, unknown)
	}
	return langs
}

// fixtureLines returns the lines of a fixture template, numbered from 1 at
// index 1.
func fixtureLines(tb testing.TB, name string) []string {
	tb.Helper()
	content := strings.TrimRight(string(readFile(tb, filepath.Join(fixtureDir, filepath.FromSlash(name)))), "\n")
	return append([]string{""}, strings.Split(content, "\n")...)
}

// sectionOf returns the section header and body lines as the script prints
// them.
func sectionOf(template string, lines []string) string {
	return "\n# github/gitignore/" + template + "\n" + strings.Join(lines, "\n") + "\n"
}

// splitGolden returns the header and the Go section of the golden file.
func splitGolden(tb testing.TB) (golden, header, goSection string) {
	tb.Helper()
	golden = string(readFile(tb, goldenIgnore))
	header, goSection, ok := strings.Cut(golden, "\n# github/gitignore/Go\n")
	if !ok {
		tb.Fatalf("%s has no Go section", goldenIgnore)
	}
	return golden, header, "\n# github/gitignore/Go\n" + goSection
}

func TestCompose(t *testing.T) {
	golden, header, goSection := splitGolden(t)

	// The script deletes file lines 22 to 28 when Rust is the first language:
	// template lines 18 to 24, the blank line and the RustRover block.
	rust := fixtureLines(t, "Rust.gitignore")
	rustSection := sectionOf("Rust", rust[3:18])
	goLines := fixtureLines(t, "Go.gitignore")
	python := fixtureLines(t, "Python.gitignore")
	jetBrains := fixtureLines(t, "Global/JetBrains.gitignore")
	golangSection := "\n# github/gitignore/community/Golang\n" +
		"# Ignore everything\n*\n\n" +
		"!/.gitattributes\n!/.gitignore\n\n" +
		"!*.go\n!go.sum\n!go.mod\n\n" +
		"!README.md\n!LICENSE\n\n" +
		"!Makefile\n\n" +
		"!*/\n"

	tests := map[string]struct {
		author string
		args   []string
		want   string
	}{
		"success: Go equals the golden file": {
			author: "git-gen",
			args:   []string{"Go"},
			want:   golden,
		},
		"success: go equals the golden file": {
			author: "git-gen",
			args:   []string{"go"},
			want:   golden,
		},
		"success: go Go gives one Go section": {
			author: "git-gen",
			args:   []string{"go", "Go"},
			want:   golden,
		},
		"success: go-pkg go gives the edited Go section": {
			author: "git-gen",
			args:   []string{"go-pkg", "go"},
			want:   golden,
		},
		"success: go-pkg gives the Go template without edits": {
			author: "git-gen",
			args:   []string{"go-pkg"},
			want:   header + sectionOf("Go", goLines[3:]),
		},
		"success: go-simple go-pkg gives one section without edits": {
			author: "git-gen",
			args:   []string{"go-simple", "go-pkg"},
			want:   header + sectionOf("Go", goLines[3:]),
		},
		"success: go Rust": {
			author: "git-gen",
			args:   []string{"go", "Rust"},
			want:   golden + rustSection,
		},
		"success: Rust go": {
			author: "git-gen",
			args:   []string{"Rust", "go"},
			want:   header + rustSection + goSection,
		},
		"success: Python keeps every line": {
			author: "git-gen",
			args:   []string{"Python"},
			want:   header + sectionOf("Python", python[1:]),
		},
		"success: nested template name": {
			author: "git-gen",
			args:   []string{"Global/JetBrains"},
			want:   header + sectionOf("Global/JetBrains", jetBrains[1:]),
		},
		"success: symlinked template keeps its own name": {
			author: "git-gen",
			args:   []string{"Alias"},
			want:   header + sectionOf("Alias", goLines[1:]),
		},
		"success: community/Golang": {
			author: "git-gen",
			args:   []string{"community/Golang"},
			want:   header + golangSection,
		},
		"success: no languages gives the header only": {
			author: "git-gen",
			want:   header,
		},
		"success: author appears in the first line only": {
			author: "Other Author",
			args:   []string{"Go"},
			want:   strings.Replace(golden, "# git-gen project", "# Other Author project", 1),
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := openCatalog(t, fixtureDir)
			got, warnings, err := c.Compose(tt.author, resolve(t, c, tt.args...))
			if err != nil {
				t.Fatalf("Compose(%q): %v", tt.args, err)
			}
			if len(warnings) != 0 {
				t.Errorf("Compose(%q) warnings = %q, want none", tt.args, warnings)
			}
			if diff := gocmp.Diff(tt.want, string(got)); diff != "" {
				t.Errorf("Compose(%q) mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

// TestComposeRust is AC12: the RustRover block is gone in either order.
func TestComposeRust(t *testing.T) {
	tests := map[string]struct {
		args []string
	}{
		"success: go Rust": {args: []string{"go", "Rust"}},
		"success: Rust go": {args: []string{"Rust", "go"}},
		"success: Rust":    {args: []string{"Rust"}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := openCatalog(t, fixtureDir)
			got, _, err := c.Compose("git-gen", resolve(t, c, tt.args...))
			if err != nil {
				t.Fatalf("Compose(%q): %v", tt.args, err)
			}
			for _, s := range []string{"RustRover", "#.idea/"} {
				if bytes.Contains(got, []byte(s)) {
					t.Errorf("Compose(%q) contains %q:\n%s", tt.args, s, got)
				}
			}
			if !bytes.Contains(got, []byte("\nrustc-ice-*.txt\n")) {
				t.Errorf("Compose(%q) lost the line before the RustRover block:\n%s", tt.args, got)
			}
		})
	}
}

// TestComposePython is AC13: Python loses no line from its top.
func TestComposePython(t *testing.T) {
	c := openCatalog(t, fixtureDir)
	got, _, err := c.Compose("git-gen", resolve(t, c, "go", "Python"))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	_, section, ok := bytes.Cut(got, []byte("\n# github/gitignore/Python\n"))
	if !ok {
		t.Fatalf("Compose output has no Python section:\n%s", got)
	}
	upstream := fixtureLines(t, "Python.gitignore")
	gotLines := strings.SplitN(string(section), "\n", 3)
	if diff := gocmp.Diff(upstream[1:3], gotLines[:2]); diff != "" {
		t.Errorf("first two lines of the Python section mismatch (-want +got):\n%s", diff)
	}
	if gotLines[1] != "__pycache__/" {
		t.Errorf("second line of the Python section = %q, want %q", gotLines[1], "__pycache__/")
	}
}

func TestComposeGolang(t *testing.T) {
	c := openCatalog(t, fixtureDir)
	got, _, err := c.Compose("git-gen", resolve(t, c, "community/Golang"))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	_, section, ok := bytes.Cut(got, []byte("\n# github/gitignore/community/Golang\n"))
	if !ok {
		t.Fatalf("Compose output has no community/Golang section:\n%s", got)
	}
	if bytes.HasPrefix(section, []byte("# files, developer configurations or IDE-specific files etc.")) {
		t.Errorf("section starts with the dangling line of the leading comment block:\n%s", section)
	}
	for _, line := range []string{"!/.gitattributes", "!Makefile"} {
		if !slices.Contains(strings.Split(string(section), "\n"), line) {
			t.Errorf("section has no line %q:\n%s", line, section)
		}
	}
}

func TestComposeDuplicates(t *testing.T) {
	c := openCatalog(t, fixtureDir)
	got, _, err := c.Compose("git-gen", resolve(t, c, "go", "Go", "go-pkg", "go-simple", "Go"))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if n := bytes.Count(got, []byte("# github/gitignore/")); n != 1 {
		t.Errorf("Compose output has %d sections, want 1:\n%s", n, got)
	}
	if n := bytes.Count(got, []byte("# Compiled Object files")); n != 1 {
		t.Errorf("Compose output has %d appended Go blocks, want 1", n)
	}
}

// TestComposeUnknown is the package part of AC14: an unknown language adds
// no section.
func TestComposeUnknown(t *testing.T) {
	golden, _, _ := splitGolden(t)
	c := openCatalog(t, fixtureDir)
	langs, unknown, err := c.Resolve([]string{"Nope", "go", "go-old"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if diff := gocmp.Diff([]string{"Nope", "go-old"}, unknown); diff != "" {
		t.Errorf("unknown mismatch (-want +got):\n%s", diff)
	}
	got, warnings, err := c.Compose("git-gen", langs)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("Compose warnings = %q, want none", warnings)
	}
	if diff := gocmp.Diff(golden, string(got)); diff != "" {
		t.Errorf("Compose mismatch (-want +got):\n%s", diff)
	}
}

// TestComposeTemplates runs Compose on catalogs whose templates differ from
// upstream, to check the edits by content and their warnings.
func TestComposeTemplates(t *testing.T) {
	const header = "# git-gen project generated files to ignore\n" +
		"#  If you want to ignore files created by your editor/tools,\n" +
		"#  please consider a global .gitignore https://docs.github.com/en/get-started/git-basics/ignoring-files.\n" +
		"#  PLEASE DO NOT open a pull request to add something created by your editor or tools\n"
	tests := map[string]struct {
		files        map[string]string
		args         []string
		want         string
		wantWarnings []string
	}{
		"success: Go edits find nothing and warn": {
			files: map[string]string{"Go.gitignore": "line 1\nline 2\n*.exe\n"},
			args:  []string{"go"},
			want:  header + "\n# github/gitignore/Go\n*.exe\n" + goSuffix,
			wantWarnings: []string{
				"github/gitignore/Go: cannot uncomment vendor/: text not found, section left as it is",
				"github/gitignore/Go: cannot remove the go.work block: text not found, section left as it is",
				"github/gitignore/Go: cannot remove the .env block: text not found, section left as it is",
			},
		},
		"success: Rust without the RustRover block warns": {
			files:        map[string]string{"Rust.gitignore": "line 1\nline 2\ndebug\ntarget\n"},
			args:         []string{"Rust"},
			want:         header + "\n# github/gitignore/Rust\ndebug\ntarget\n",
			wantWarnings: []string{"github/gitignore/Rust: cannot remove the RustRover block: text not found, section left as it is"},
		},
		"success: Rust block without its last line warns": {
			files:        map[string]string{"Rust.gitignore": "line 1\nline 2\ndebug\n\n# RustRover\n# .idea/\n"},
			args:         []string{"Rust"},
			want:         header + "\n# github/gitignore/Rust\ndebug\n\n# RustRover\n# .idea/\n",
			wantWarnings: []string{"github/gitignore/Rust: cannot remove the RustRover block: text not found, section left as it is"},
		},
		"success: Rust block in the middle": {
			files: map[string]string{"Rust.gitignore": "line 1\nline 2\ndebug\n\n# RustRover\n#  comment\n#.idea/\n\n# after\nlast\n"},
			args:  []string{"Rust"},
			want:  header + "\n# github/gitignore/Rust\ndebug\n\n# after\nlast\n",
		},
		"success: Rust block at the top of the body": {
			files: map[string]string{"Rust.gitignore": "line 1\nline 2\n# RustRover\n#.idea/\nkept\n"},
			args:  []string{"Rust"},
			want:  header + "\n# github/gitignore/Rust\nkept\n",
		},
		"success: Rust block after a first blank line": {
			files: map[string]string{"Rust.gitignore": "line 1\nline 2\n\n# RustRover\n#.idea/\n\nkept\n"},
			args:  []string{"Rust"},
			want:  header + "\n# github/gitignore/Rust\n\nkept\n",
		},
		"success: allowlist edits find nothing and warn only for the first two": {
			files: map[string]string{"community/Golang/Go.AllowList.gitignore": "# comment\n#\n\n*\n!*.go\n"},
			args:  []string{"community/Golang"},
			want:  header + "\n# github/gitignore/community/Golang\n*\n!*.go\n",
			wantWarnings: []string{
				"github/gitignore/community/Golang: cannot allow /.gitattributes: text not found, section left as it is",
				"github/gitignore/community/Golang: cannot allow Makefile: text not found, section left as it is",
			},
		},
		"success: allowlist without a leading comment keeps its first blank line": {
			files: map[string]string{"community/Golang/Go.AllowList.gitignore": "\n*\n# But not these files...\n# !Makefile\n"},
			args:  []string{"community/Golang"},
			want:  header + "\n# github/gitignore/community/Golang\n\n*\n!/.gitattributes\n!Makefile\n",
		},
		"success: author placeholder in a template is kept": {
			files: map[string]string{"Foo.gitignore": "# AUTHOR notes\nAUTHOR\n"},
			args:  []string{"Foo"},
			want:  header + "\n# github/gitignore/Foo\n# AUTHOR notes\nAUTHOR\n",
		},
		"success: empty template gives an empty body": {
			files: map[string]string{"Empty.gitignore": ""},
			args:  []string{"Empty"},
			want:  header + "\n# github/gitignore/Empty\n\n",
		},
		"success: trailing newlines of a template collapse to one": {
			files: map[string]string{"Many.gitignore": "a\n\n\n\n"},
			args:  []string{"Many"},
			want:  header + "\n# github/gitignore/Many\na\n",
		},
		"success: template shorter than the lines to drop": {
			files: map[string]string{"Go.gitignore": "only line"},
			args:  []string{"go-pkg"},
			want:  header + "\n# github/gitignore/Go\n\n",
		},
		"success: allowlist outside the table keeps every line": {
			files: map[string]string{"Foo/Foo.AllowList.gitignore": "# comment\n\n*\n"},
			args:  []string{"Foo"},
			want:  header + "\n# github/gitignore/Foo\n# comment\n\n*\n",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := openCatalog(t, writeTree(t, t.TempDir(), tt.files))
			got, warnings, err := c.Compose("git-gen", resolve(t, c, tt.args...))
			if err != nil {
				t.Fatalf("Compose(%q): %v", tt.args, err)
			}
			if diff := gocmp.Diff(tt.want, string(got)); diff != "" {
				t.Errorf("Compose(%q) mismatch (-want +got):\n%s", tt.args, diff)
			}
			if diff := gocmp.Diff(tt.wantWarnings, warnings); diff != "" {
				t.Errorf("Compose(%q) warnings mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

func TestComposeError(t *testing.T) {
	tests := map[string]struct {
		langs       []Language
		wantText    string
		wantInvalid bool
	}{
		"error: language that resolves to no template": {
			langs:    []Language{{Arg: "go"}, {Arg: "Nope", Template: "Nope"}},
			wantText: `gitignore: no template for "Nope"`,
		},
		"error: language with an invalid name": {
			langs:       []Language{{Arg: "../escape", Template: "../escape"}},
			wantText:    `gitignore: invalid template name "../escape"`,
			wantInvalid: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := openCatalog(t, fixtureDir)
			got, warnings, err := c.Compose("git-gen", tt.langs)
			if err == nil {
				t.Fatalf("Compose(%v) = %q, want an error", tt.langs, got)
			}
			if got != nil || warnings != nil {
				t.Errorf("Compose(%v) = %q, %q with error; want nil, nil", tt.langs, got, warnings)
			}
			if err.Error() != tt.wantText {
				t.Errorf("Compose(%v) error = %q, want %q", tt.langs, err, tt.wantText)
			}
			var invalid *InvalidNameError
			if got := errors.As(err, &invalid); got != tt.wantInvalid {
				t.Errorf("errors.As(%v, *InvalidNameError) = %v, want %v", err, got, tt.wantInvalid)
			}
		})
	}
}

func TestTrim(t *testing.T) {
	tests := map[string]struct {
		trim func([]byte) []byte
		in   string
		want string
	}{
		"success: drop two lines":                          {trim: dropLines(2), in: "a\nb\nc\nd\n", want: "c\nd\n"},
		"success: drop two lines of two":                   {trim: dropLines(2), in: "a\nb\n", want: ""},
		"success: drop two lines of one without a newline": {trim: dropLines(2), in: "a", want: ""},
		"success: drop no lines":                           {trim: dropLines(0), in: "a\n", want: "a\n"},
		"success: comment block and the blank line after":  {trim: dropLeadingComment, in: "# a\n#\n# b\n\nkept\n", want: "kept\n"},
		"success: comment block without a blank line":      {trim: dropLeadingComment, in: "# a\nkept\n", want: "kept\n"},
		"success: only one blank line goes":                {trim: dropLeadingComment, in: "# a\n\n\nkept\n", want: "\nkept\n"},
		"success: no comment block keeps a blank line":     {trim: dropLeadingComment, in: "\nkept\n", want: "\nkept\n"},
		"success: comment block only":                      {trim: dropLeadingComment, in: "# a\n# b", want: ""},
		"success: comment lines later are kept":            {trim: dropLeadingComment, in: "kept\n# later\n", want: "kept\n# later\n"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if diff := gocmp.Diff(tt.want, string(tt.trim([]byte(tt.in)))); diff != "" {
				t.Errorf("trim(%q) mismatch (-want +got):\n%s", tt.in, diff)
			}
		})
	}
}

func TestDropRustRover(t *testing.T) {
	tests := map[string]struct {
		in         string
		want       string
		wantMissed []string
	}{
		"success: block at the end with a final newline": {
			in:   "a\n\n# RustRover\n#.idea/\n",
			want: "a\n",
		},
		"success: block at the end without a final newline": {
			in:   "a\n\n# RustRover\n#.idea/",
			want: "a\n",
		},
		"success: line that only starts with the anchor is not the anchor": {
			in:         "# RustRover IDE\n#.idea/\n",
			want:       "# RustRover IDE\n#.idea/\n",
			wantMissed: []string{"remove the RustRover block"},
		},
		"success: last line before the first line does not count": {
			in:         "#.idea/\n# RustRover\n",
			want:       "#.idea/\n# RustRover\n",
			wantMissed: []string{"remove the RustRover block"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, missed := dropRustRover([]byte(tt.in))
			if diff := gocmp.Diff(tt.want, string(got)); diff != "" {
				t.Errorf("dropRustRover(%q) mismatch (-want +got):\n%s", tt.in, diff)
			}
			if diff := gocmp.Diff(tt.wantMissed, missed); diff != "" {
				t.Errorf("dropRustRover(%q) missed mismatch (-want +got):\n%s", tt.in, diff)
			}
		})
	}
}

func BenchmarkCompose(b *testing.B) {
	c := openCatalog(b, fixtureDir)
	langs := resolve(b, c, "Go", "Rust", "Python", "community/Golang", "Global/JetBrains")
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := c.Compose("git-gen", langs); err != nil {
			b.Fatal(err)
		}
	}
}
