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
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

// fixtureDir has the layout of the owner's boilerplate directory, so that the
// end-to-end tests can point XDG_CONFIG_HOME at the testdata directory.
var fixtureDir = filepath.Join("..", "..", "testdata", "boilerplate")

var testVars = Vars{
	Author:       "gopher",
	Organization: "acme",
	Project:      "widget",
	Contact:      "conduct@example.com",
	SPDXID:       "Apache-2.0",
	Year:         2026,
}

// githubFiles are the five files that Set.Go places under .github/.
var githubFiles = []string{
	".github/CODEOWNERS",
	".github/ISSUE_TEMPLATE/bug_report.yml",
	".github/PULL_REQUEST_TEMPLATE.md",
	".github/dependabot.yaml",
	".github/renovate.json5",
}

// fixture copies the fixture tree into a temporary directory and adds what git
// cannot hold in the repository: .DS_Store files and an empty workflows/.
func fixture(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "boilerplate")
	if err := os.CopyFS(dir, os.DirFS(fixtureDir)); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	writeFile(t, dir, ".github/.DS_Store", "\x00\x00\x00\x01Bud1")
	writeFile(t, dir, ".github/ISSUE_TEMPLATE/.DS_Store", "\x00\x00\x00\x01Bud1")
	if err := os.Mkdir(filepath.Join(dir, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}

	return dir
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()

	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()

	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}

func paths(files []File) []string {
	ps := make([]string, 0, len(files))
	for _, f := range files {
		ps = append(ps, f.Path)
	}

	return ps
}

func find(t *testing.T, files []File, p string) File {
	t.Helper()

	i := slices.IndexFunc(files, func(f File) bool { return f.Path == p })
	if i < 0 {
		t.Fatalf("Render returned no %s; got %q", p, paths(files))
	}

	return files[i]
}

// tree returns the regular files and the directories under root, sorted.
func tree(t *testing.T, root string) (files, dirs []string) {
	t.Helper()

	err := fs.WalkDir(os.DirFS(root), ".", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case p == ".":
		case d.IsDir():
			dirs = append(dirs, p)
		default:
			files = append(files, p)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	return files, dirs
}

// parents returns every directory that holds one of files, sorted.
func parents(files []string) []string {
	var dirs []string
	for _, f := range files {
		for d := path.Dir(f); d != "."; d = path.Dir(d) {
			if !slices.Contains(dirs, d) {
				dirs = append(dirs, d)
			}
		}
	}
	slices.Sort(dirs)

	return dirs
}

func TestDir(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		env  map[string]string
		home string
		want string
	}{
		"success: XDG_CONFIG_HOME set": {
			env:  map[string]string{"XDG_CONFIG_HOME": "/xdg/config"},
			home: "/home/gopher",
			want: filepath.Join("/xdg/config", "boilerplate"),
		},
		"success: XDG_CONFIG_HOME empty falls back to home": {
			env:  map[string]string{"XDG_CONFIG_HOME": ""},
			home: "/home/gopher",
			want: filepath.Join("/home/gopher", ".config", "boilerplate"),
		},
		"success: XDG_CONFIG_HOME unset falls back to home": {
			home: "/home/gopher",
			want: filepath.Join("/home/gopher", ".config", "boilerplate"),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := Dir(func(key string) string { return tt.env[key] }, tt.home)
			if diff := gocmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Dir() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestRenderWriteSets checks the placed files for every combination of Set,
// and the tree that Write leaves behind.
func TestRenderWriteSets(t *testing.T) {
	t.Parallel()

	base := []string{"CODE_OF_CONDUCT.md", "README.md"}
	goFiles := append(slices.Clone(githubFiles), ".golangci.yaml")
	makeFiles := []string{"Makefile"}
	hackFiles := []string{"hack/boilerplate/boilerplate.go.txt"}

	tests := map[string]struct {
		set  Set
		want []string
	}{
		"success: no optional group": {
			set:  Set{},
			want: base,
		},
		"success: Go": {
			set:  Set{Go: true},
			want: slices.Concat(base, goFiles),
		},
		"success: Makefile": {
			set:  Set{Makefile: true},
			want: slices.Concat(base, makeFiles),
		},
		"success: Hack": {
			set:  Set{Hack: true},
			want: slices.Concat(base, hackFiles),
		},
		"success: Go and Makefile": {
			set:  Set{Go: true, Makefile: true},
			want: slices.Concat(base, goFiles, makeFiles),
		},
		"success: Go and Hack": {
			set:  Set{Go: true, Hack: true},
			want: slices.Concat(base, goFiles, hackFiles),
		},
		"success: Makefile and Hack": {
			set:  Set{Makefile: true, Hack: true},
			want: slices.Concat(base, makeFiles, hackFiles),
		},
		"success: Go, Makefile and Hack": {
			set:  Set{Go: true, Makefile: true, Hack: true},
			want: slices.Concat(base, goFiles, makeFiles, hackFiles),
		},
	}

	dir := fixture(t)
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			want := slices.Sorted(slices.Values(tt.want))

			files, warnings, err := Render(dir, tt.set, testVars)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if len(warnings) != 0 {
				t.Errorf("Render() warnings = %q, want none", warnings)
			}
			if diff := gocmp.Diff(want, paths(files)); diff != "" {
				t.Errorf("Render() paths mismatch (-want +got):\n%s", diff)
			}

			root := t.TempDir()
			written, skipped, err := Write(root, files)
			if err != nil {
				t.Fatalf("Write() error = %v", err)
			}
			if diff := gocmp.Diff(want, written); diff != "" {
				t.Errorf("Write() written mismatch (-want +got):\n%s", diff)
			}
			if len(skipped) != 0 {
				t.Errorf("Write() skipped = %q, want none", skipped)
			}

			// Exact equality rules out .github/CODE_OF_CONDUCT.md, .DS_Store,
			// and, through the directory list, an empty workflows/.
			gotFiles, gotDirs := tree(t, root)
			if diff := gocmp.Diff(want, gotFiles); diff != "" {
				t.Errorf("files on disk mismatch (-want +got):\n%s", diff)
			}
			if diff := gocmp.Diff(parents(want), gotDirs); diff != "" {
				t.Errorf("directories on disk mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestRenderContent checks each rendered file against its template with the
// placeholders replaced (the three module paths for .golangci.yaml).
func TestRenderContent(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		output   string
		template string
		want     func(t *testing.T, src string) string
	}{
		"success: golangci-lint template changes only the three placeholder lines": {
			output:   ".golangci.yaml",
			template: "go/.golangci.yaml",
			want: func(t *testing.T, src string) string {
				t.Helper()

				replace := map[string]string{
					"        - prefix(github.com/)":  "        - prefix(github.com/acme/widget)",
					"      module-path: github.com/": "      module-path: github.com/acme/widget",
					"        - github.com/":          "        - github.com/acme/widget",
				}
				for _, keep := range []string{
					"    #     Copyright {{ YEAR }} The example Authors.",
					"    - testifylint # Checks usage of github.com/stretchr/testify. [auto-fix]",
				} {
					if !slices.Contains(strings.Split(src, "\n"), keep) {
						t.Fatalf("fixture lacks the line %q", keep)
					}
				}
				lines := strings.Split(src, "\n")
				changed := 0
				for i, line := range lines {
					if repl, ok := replace[line]; ok {
						lines[i] = repl
						changed++
					}
				}
				if changed != len(replace) {
					t.Fatalf("fixture has %d placeholder lines, want %d", changed, len(replace))
				}

				return strings.Join(lines, "\n")
			},
		},
		"success: every PKG of the bug report becomes the project": {
			output:   ".github/ISSUE_TEMPLATE/bug_report.yml",
			template: ".github/ISSUE_TEMPLATE/bug_report.yml",
			want: func(t *testing.T, src string) string {
				t.Helper()

				if n := strings.Count(src, "PKG"); n != 3 {
					t.Fatalf("fixture has %d PKG, want 3", n)
				}

				return strings.ReplaceAll(src, "PKG", "widget")
			},
		},
		"success: the hack header carries the year and the author": {
			output:   "hack/boilerplate/boilerplate.go.txt",
			template: "go/boilerplate.go.txt",
			want: func(t *testing.T, src string) string {
				t.Helper()

				first, rest, ok := strings.Cut(src, "\n")
				if !ok || first != "// Copyright YEAR The AUTHOR Authors." {
					t.Fatalf("fixture first line = %q", first)
				}

				return "// Copyright 2026 The gopher Authors.\n" + rest
			},
		},
		"success: the code of conduct carries the contact": {
			output:   "CODE_OF_CONDUCT.md",
			template: ".github/CODE_OF_CONDUCT.md",
			want: func(t *testing.T, src string) string {
				t.Helper()

				return strings.Replace(src, "[INSERT CONTACT METHOD]", "conduct@example.com", 1)
			},
		},
		"success: templates without placeholders are copied verbatim": {
			output:   "Makefile",
			template: "go/Makefile",
			want: func(t *testing.T, src string) string {
				t.Helper()

				return src
			},
		},
		"success: README.md is the project title": {
			output: "README.md",
			want: func(t *testing.T, _ string) string {
				t.Helper()

				return "# widget\n"
			},
		},
	}

	files, warnings, err := Render(fixture(t), Set{Go: true, Makefile: true, Hack: true}, testVars)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("Render() warnings = %q, want none", warnings)
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var src string
			if tt.template != "" {
				src = readFile(t, fixtureDir, tt.template)
			}
			want := tt.want(t, src)
			got := find(t, files, tt.output)
			if diff := gocmp.Diff(want, string(got.Content)); diff != "" {
				t.Errorf("%s mismatch (-want +got):\n%s", tt.output, diff)
			}
		})
	}
}

// TestRenderRules replaces one template and checks the rendered file and the
// warnings of the required rules that matched nothing.
func TestRenderRules(t *testing.T) {
	t.Parallel()

	variant := readFile(t, fixtureDir, "variant/missing-module-path/go/.golangci.yaml")

	tests := map[string]struct {
		template  string
		content   string
		vars      Vars
		output    string
		want      string
		wantRules []string
	}{
		"success: golangci-lint template without the module-path placeholder warns": {
			template: "go/.golangci.yaml",
			content:  variant,
			vars:     testVars,
			output:   ".golangci.yaml",
			want: strings.NewReplacer(
				"prefix(github.com/)", "prefix(github.com/acme/widget)",
				"        - github.com/\n", "        - github.com/acme/widget\n",
			).Replace(variant),
			wantRules: []string{"gofumpt module-path: github.com/"},
		},
		"success: golangci-lint template without any placeholder warns": {
			template: "go/.golangci.yaml",
			content:  "version: \"2\"\n# module-path: github.com/\n",
			vars:     testVars,
			output:   ".golangci.yaml",
			want:     "version: \"2\"\n# module-path: github.com/\n",
			wantRules: []string{
				"gci prefix(github.com/)",
				"gofumpt module-path: github.com/",
				"goimports local-prefixes item - github.com/",
			},
		},
		"success: local-prefixes item after another item": {
			template: "go/.golangci.yaml",
			content:  "gci:\n  - prefix(github.com/)\ngofumpt:\n  module-path: github.com/\ngoimports:\n  local-prefixes:\n    - example.com/other\n    - github.com/\nlinters:\n  - github.com/\n",
			vars:     testVars,
			output:   ".golangci.yaml",
			want:     "gci:\n  - prefix(github.com/acme/widget)\ngofumpt:\n  module-path: github.com/acme/widget\ngoimports:\n  local-prefixes:\n    - example.com/other\n    - github.com/acme/widget\nlinters:\n  - github.com/\n",
		},
		"success: CRLF line endings are kept": {
			template: "go/.golangci.yaml",
			content:  "- prefix(github.com/)\r\nmodule-path: github.com/\r\nlocal-prefixes:\r\n  - github.com/\r\n",
			vars:     testVars,
			output:   ".golangci.yaml",
			want:     "- prefix(github.com/acme/widget)\r\nmodule-path: github.com/acme/widget\r\nlocal-prefixes:\r\n  - github.com/acme/widget\r\n",
		},
		"success: bug report without PKG warns": {
			template:  ".github/ISSUE_TEMPLATE/bug_report.yml",
			content:   "name: Bug Report\ntitle: \"pkg name: \"\nlabel: PKGS\n",
			vars:      testVars,
			output:    ".github/ISSUE_TEMPLATE/bug_report.yml",
			want:      "name: Bug Report\ntitle: \"pkg name: \"\nlabel: PKGS\n",
			wantRules: []string{"PKG"},
		},
		"success: code of conduct without the contact placeholder warns": {
			template:  ".github/CODE_OF_CONDUCT.md",
			content:   "Report to the maintainers.\n",
			vars:      testVars,
			output:    "CODE_OF_CONDUCT.md",
			want:      "Report to the maintainers.\n",
			wantRules: []string{"[INSERT CONTACT METHOD]"},
		},
		"success: hack header without AUTHOR and YEAR warns for both, not for LICENSE_IDENTIFIER": {
			template:  "go/boilerplate.go.txt",
			content:   "// Copyright THE AUTHORS, YEARS AGO.\n",
			vars:      testVars,
			output:    "hack/boilerplate/boilerplate.go.txt",
			want:      "// Copyright THE AUTHORS, YEARS AGO.\n",
			wantRules: []string{"AUTHOR", "YEAR"},
		},
		"success: tokens match only as whole words": {
			template: "go/boilerplate.go.txt",
			content:  "AUTHOR AUTHORS _AUTHOR AUTHOR_X YEARLY (YEAR) SPDX-License-Identifier: LICENSE_IDENTIFIER\n",
			vars:     testVars,
			output:   "hack/boilerplate/boilerplate.go.txt",
			want:     "gopher AUTHORS _AUTHOR AUTHOR_X YEARLY (2026) SPDX-License-Identifier: Apache-2.0\n",
		},
		"success: values are inserted literally and never rewritten": {
			template: "go/boilerplate.go.txt",
			content:  "// Copyright YEAR The AUTHOR Authors.\n",
			vars:     Vars{Author: `YEAR $1 \ LICENSE_IDENTIFIER`, SPDXID: "MIT", Year: 2026},
			output:   "hack/boilerplate/boilerplate.go.txt",
			want:     "// Copyright 2026 The YEAR $1 \\ LICENSE_IDENTIFIER Authors.\n",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := fixture(t)
			writeFile(t, dir, tt.template, tt.content)

			files, warnings, err := Render(dir, Set{Go: true, Makefile: true, Hack: true}, tt.vars)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			got := find(t, files, tt.output)
			if diff := gocmp.Diff(tt.want, string(got.Content)); diff != "" {
				t.Errorf("%s mismatch (-want +got):\n%s", tt.output, diff)
			}

			if len(warnings) != len(tt.wantRules) {
				t.Fatalf("Render() warnings = %q, want one per rule of %q", warnings, tt.wantRules)
			}
			template := filepath.Join(dir, filepath.FromSlash(tt.template))
			for i, w := range warnings {
				if !strings.Contains(w, template) || !strings.Contains(w, strconv.Quote(tt.wantRules[i])) {
					t.Errorf("warning %d = %q, want it to name %s and rule %q", i, w, template, tt.wantRules[i])
				}
			}
		})
	}
}

func TestRenderModesAndLinks(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	if err := os.Chmod(filepath.Join(dir, "go", "Makefile"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, ".github", "CODEOWNERS"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "shared/SECURITY.md", "# Security\n")
	if err := os.Symlink(filepath.Join("..", "shared", "SECURITY.md"), filepath.Join(dir, ".github", "SECURITY.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "shared"), filepath.Join(dir, ".github", "linked-dir")); err != nil {
		t.Fatal(err)
	}

	files, _, err := Render(dir, Set{Go: true, Makefile: true}, testVars)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	want := slices.Sorted(slices.Values(slices.Concat(
		[]string{"CODE_OF_CONDUCT.md", "README.md", ".golangci.yaml", "Makefile", ".github/SECURITY.md"},
		githubFiles,
	)))
	if diff := gocmp.Diff(want, paths(files)); diff != "" {
		t.Errorf("Render() paths mismatch (-want +got):\n%s", diff)
	}

	modes := map[string]fs.FileMode{
		"Makefile":           0o755,
		".github/CODEOWNERS": 0o600,
		"README.md":          0o644,
	}
	for p, want := range modes {
		if got := find(t, files, p).Mode; got != want {
			t.Errorf("%s mode = %v, want %v", p, got, want)
		}
	}
	if got := string(find(t, files, ".github/SECURITY.md").Content); got != "# Security\n" {
		t.Errorf(".github/SECURITY.md content = %q, want the link target", got)
	}
}

// TestRenderLinkConfinement checks that a template link is followed only while its target stays inside
// the boilerplate directory, so that no file outside it is copied into a repository and committed. The
// directory itself may be reached through a link, as ~/.config often is.
func TestRenderLinkConfinement(t *testing.T) {
	t.Parallel()

	const secret = "a line from outside the boilerplate directory\n"
	tests := map[string]struct {
		set Set
		// link returns the template path, slash-separated, that is replaced with a link to target, and the
		// value of that link.
		link func(target string) (name, value string)
		// wantErrPath is the template that the error must name; empty means that Render succeeds.
		wantErrPath string
	}{
		"error: a .github template linked to an absolute path outside": {
			set:         Set{Go: true},
			link:        func(target string) (string, string) { return ".github/PULL_REQUEST_TEMPLATE.md", target },
			wantErrPath: ".github/PULL_REQUEST_TEMPLATE.md",
		},
		"error: the code of conduct linked to a relative path outside": {
			set: Set{},
			link: func(string) (string, string) {
				return ".github/CODE_OF_CONDUCT.md", filepath.Join("..", "..", "outside", "secret.txt")
			},
			wantErrPath: ".github/CODE_OF_CONDUCT.md",
		},
		"error: the Makefile linked outside": {
			set:         Set{Makefile: true},
			link:        func(target string) (string, string) { return "go/Makefile", target },
			wantErrPath: "go/Makefile",
		},
		"success: a link inside the directory is followed": {
			set: Set{Go: true},
			link: func(string) (string, string) {
				return ".github/renovate.json5", "dependabot.yaml"
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := fixture(t)
			target := filepath.Join(filepath.Dir(dir), "outside", "secret.txt")
			writeFile(t, filepath.Dir(target), filepath.Base(target), secret)
			linkName, value := tt.link(target)
			linkPath := filepath.Join(dir, filepath.FromSlash(linkName))
			if err := os.Remove(linkPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(value, linkPath); err != nil {
				t.Fatal(err)
			}

			files, _, err := Render(dir, tt.set, testVars)
			if tt.wantErrPath == "" {
				if err != nil {
					t.Fatalf("Render() error = %v", err)
				}
				want, err := os.ReadFile(filepath.Join(dir, ".github", "dependabot.yaml"))
				if err != nil {
					t.Fatal(err)
				}
				if got := find(t, files, linkName).Content; string(got) != string(want) {
					t.Errorf("%s content = %q, want the content of its target %q", linkName, got, want)
				}
				return
			}
			if err == nil {
				t.Fatalf("Render() = %q, want an error for the link that leaves the directory", paths(files))
			}
			if want := filepath.Join(dir, filepath.FromSlash(tt.wantErrPath)); !strings.Contains(err.Error(), want) {
				t.Errorf("Render() error = %q, want it to name %s", err, want)
			}
			if files != nil {
				t.Errorf("Render() files = %q, want nil with the error", paths(files))
			}
			for _, f := range files {
				if strings.Contains(string(f.Content), secret) {
					t.Errorf("%s carries the content of a file outside the directory", f.Path)
				}
			}
		})
	}
}

// TestRenderLinkedDir checks that the boilerplate directory itself may be a link with an absolute target.
func TestRenderLinkedDir(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		set  Set
		want []string
	}{
		"success: Go templates through a linked directory": {
			set:  Set{Go: true, Makefile: true, Hack: true},
			want: slices.Concat([]string{".golangci.yaml", "CODE_OF_CONDUCT.md", "Makefile", "README.md", "hack/boilerplate/boilerplate.go.txt"}, githubFiles),
		},
		"success: the code of conduct alone through a linked directory": {
			set:  Set{},
			want: []string{"CODE_OF_CONDUCT.md", "README.md"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			target := fixture(t)
			if !filepath.IsAbs(target) {
				t.Fatalf("fixture %q is not absolute", target)
			}
			config := filepath.Join(t.TempDir(), ".config")
			if err := os.Symlink(filepath.Dir(target), config); err != nil {
				t.Fatal(err)
			}

			files, warnings, err := Render(filepath.Join(config, "boilerplate"), tt.set, testVars)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if len(warnings) != 0 {
				t.Errorf("Render() warnings = %q, want none", warnings)
			}
			if diff := gocmp.Diff(slices.Sorted(slices.Values(tt.want)), paths(files)); diff != "" {
				t.Errorf("Render() paths mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRenderErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		set      Set
		prepare  func(t *testing.T, dir string)
		notExist bool
	}{
		"error: code of conduct template missing": {
			set: Set{},
			prepare: func(t *testing.T, dir string) {
				t.Helper()

				if err := os.Remove(filepath.Join(dir, ".github", "CODE_OF_CONDUCT.md")); err != nil {
					t.Fatal(err)
				}
			},
			notExist: true,
		},
		"error: the boilerplate directory does not exist": {
			set: Set{},
			prepare: func(t *testing.T, dir string) {
				t.Helper()

				if err := os.RemoveAll(dir); err != nil {
					t.Fatal(err)
				}
			},
			notExist: true,
		},
		"error: dangling link under .github": {
			set: Set{Go: true},
			prepare: func(t *testing.T, dir string) {
				t.Helper()

				if err := os.Symlink("nowhere", filepath.Join(dir, ".github", "dangling.md")); err != nil {
					t.Fatal(err)
				}
			},
			notExist: true,
		},
		"error: golangci-lint template is a directory": {
			set: Set{Go: true},
			prepare: func(t *testing.T, dir string) {
				t.Helper()

				p := filepath.Join(dir, "go", ".golangci.yaml")
				if err := os.Remove(p); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(p, 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
		"error: Makefile template missing": {
			set: Set{Makefile: true},
			prepare: func(t *testing.T, dir string) {
				t.Helper()

				if err := os.Remove(filepath.Join(dir, "go", "Makefile")); err != nil {
					t.Fatal(err)
				}
			},
			notExist: true,
		},
		"error: hack header template missing": {
			set: Set{Hack: true},
			prepare: func(t *testing.T, dir string) {
				t.Helper()

				if err := os.Remove(filepath.Join(dir, "go", "boilerplate.go.txt")); err != nil {
					t.Fatal(err)
				}
			},
			notExist: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := fixture(t)
			tt.prepare(t, dir)

			files, warnings, err := Render(dir, tt.set, testVars)
			if err == nil {
				t.Fatalf("Render() = %q, %q, nil; want an error", paths(files), warnings)
			}
			if got := errors.Is(err, fs.ErrNotExist); got != tt.notExist {
				t.Errorf("errors.Is(%v, fs.ErrNotExist) = %t, want %t", err, got, tt.notExist)
			}
			if files != nil || warnings != nil {
				t.Errorf("Render() returned %q and %q with an error, want nil", paths(files), warnings)
			}
		})
	}
}

// TestMissing checks which missing templates Missing reports for each Set.
func TestMissing(t *testing.T) {
	t.Parallel()

	all := Set{Go: true, Makefile: true, Hack: true}

	tests := map[string]struct {
		set    Set
		remove []string
		asDir  []string
		want   []string
	}{
		"success: nothing missing": {
			set: all,
		},
		"error: golangci-lint template missing for Go": {
			set:    Set{Go: true},
			remove: []string{"go/.golangci.yaml"},
			want:   []string{"go/.golangci.yaml"},
		},
		"success: golangci-lint template missing without Go": {
			set:    Set{Makefile: true, Hack: true},
			remove: []string{"go/.golangci.yaml"},
		},
		"error: bug report missing for Go": {
			set:    Set{Go: true},
			remove: []string{".github/ISSUE_TEMPLATE/bug_report.yml"},
			want:   []string{".github/ISSUE_TEMPLATE/bug_report.yml"},
		},
		"error: go directory missing for Makefile and Hack": {
			set:    Set{Makefile: true, Hack: true},
			remove: []string{"go"},
			want:   []string{"go/Makefile", "go/boilerplate.go.txt"},
		},
		"error: code of conduct missing for every set": {
			set:    Set{},
			remove: []string{".github/CODE_OF_CONDUCT.md"},
			want:   []string{".github/CODE_OF_CONDUCT.md"},
		},
		"error: template that is a directory counts as missing": {
			set:   Set{Makefile: true},
			asDir: []string{"go/Makefile"},
			want:  []string{"go/Makefile"},
		},
		"error: whole directory missing": {
			set:    all,
			remove: []string{"."},
			want: []string{
				".github/CODE_OF_CONDUCT.md",
				".github/ISSUE_TEMPLATE/bug_report.yml",
				"go/.golangci.yaml",
				"go/Makefile",
				"go/boilerplate.go.txt",
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := fixture(t)
			for _, p := range tt.remove {
				if err := os.RemoveAll(filepath.Join(dir, filepath.FromSlash(p))); err != nil {
					t.Fatal(err)
				}
			}
			for _, p := range tt.asDir {
				full := filepath.Join(dir, filepath.FromSlash(p))
				if err := os.Remove(full); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(full, 0o755); err != nil {
					t.Fatal(err)
				}
			}

			var want []string
			for _, p := range tt.want {
				want = append(want, filepath.Join(dir, filepath.FromSlash(p)))
			}
			if diff := gocmp.Diff(want, Missing(dir, tt.set)); diff != "" {
				t.Errorf("Missing() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestMissingRelativeDir is not parallel: it changes the working directory.
func TestMissingRelativeDir(t *testing.T) {
	dir := fixture(t)
	if err := os.Remove(filepath.Join(dir, "go", "Makefile")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Dir(dir))
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	want := []string{filepath.Join(wd, "boilerplate", "go", "Makefile")}
	if diff := gocmp.Diff(want, Missing("boilerplate", Set{Makefile: true})); diff != "" {
		t.Errorf("Missing() mismatch (-want +got):\n%s", diff)
	}
}

// TestWrite checks that Write places only missing files, also into an existing .github/.
func TestWrite(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		existing    map[string]string
		existingDir []string
		files       []File
		wantWritten []string
		wantSkipped []string
		wantFiles   map[string]string
		wantErr     bool
	}{
		"success: existing files are skipped and left unchanged": {
			existing: map[string]string{
				"README.md":          "# mine\n",
				"CODE_OF_CONDUCT.md": "our own\n",
			},
			files: []File{
				{Path: "CODE_OF_CONDUCT.md", Content: []byte("template\n"), Mode: 0o644},
				{Path: "README.md", Content: []byte("# widget\n"), Mode: 0o644},
				{Path: ".github/PULL_REQUEST_TEMPLATE.md", Content: []byte("## Why\n"), Mode: 0o644},
			},
			wantWritten: []string{".github/PULL_REQUEST_TEMPLATE.md"},
			wantSkipped: []string{"CODE_OF_CONDUCT.md", "README.md"},
			wantFiles: map[string]string{
				"README.md":                        "# mine\n",
				"CODE_OF_CONDUCT.md":               "our own\n",
				".github/PULL_REQUEST_TEMPLATE.md": "## Why\n",
			},
		},
		"success: an existing .github receives only the missing files": {
			existing: map[string]string{
				".github/PULL_REQUEST_TEMPLATE.md": "keep\n",
			},
			files: []File{
				{Path: ".github/CODEOWNERS", Content: []byte("* @acme\n"), Mode: 0o644},
				{Path: ".github/PULL_REQUEST_TEMPLATE.md", Content: []byte("## Why\n"), Mode: 0o644},
			},
			wantWritten: []string{".github/CODEOWNERS"},
			wantSkipped: []string{".github/PULL_REQUEST_TEMPLATE.md"},
			wantFiles: map[string]string{
				".github/CODEOWNERS":               "* @acme\n",
				".github/PULL_REQUEST_TEMPLATE.md": "keep\n",
			},
		},
		"success: a directory at the path counts as existing": {
			existingDir: []string{"README.md"},
			files: []File{
				{Path: "README.md", Content: []byte("# widget\n"), Mode: 0o644},
			},
			wantSkipped: []string{"README.md"},
			wantFiles:   map[string]string{},
		},
		"error: path outside the root": {
			files: []File{
				{Path: "../escape.md", Content: []byte("out\n"), Mode: 0o644},
			},
			wantFiles: map[string]string{},
			wantErr:   true,
		},
		"error: parent is a regular file": {
			existing: map[string]string{
				"hack": "not a directory\n",
			},
			files: []File{
				{Path: "README.md", Content: []byte("# widget\n"), Mode: 0o644},
				{Path: "hack/boilerplate/boilerplate.go.txt", Content: []byte("// header\n"), Mode: 0o644},
			},
			wantWritten: []string{"README.md"},
			wantFiles: map[string]string{
				"README.md": "# widget\n",
				"hack":      "not a directory\n",
			},
			wantErr: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			parent := t.TempDir()
			root := filepath.Join(parent, "repo")
			if err := os.Mkdir(root, 0o755); err != nil {
				t.Fatal(err)
			}
			for p, content := range tt.existing {
				writeFile(t, root, p, content)
			}
			for _, p := range tt.existingDir {
				if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(p)), 0o755); err != nil {
					t.Fatal(err)
				}
			}

			written, skipped, err := Write(root, tt.files)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Write() error = %v, wantErr %t", err, tt.wantErr)
			}
			if diff := gocmp.Diff(tt.wantWritten, written); diff != "" {
				t.Errorf("Write() written mismatch (-want +got):\n%s", diff)
			}
			if diff := gocmp.Diff(tt.wantSkipped, skipped); diff != "" {
				t.Errorf("Write() skipped mismatch (-want +got):\n%s", diff)
			}

			gotFiles, _ := tree(t, root)
			got := make(map[string]string, len(gotFiles))
			for _, p := range gotFiles {
				got[p] = readFile(t, root, p)
			}
			if diff := gocmp.Diff(tt.wantFiles, got); diff != "" {
				t.Errorf("files on disk mismatch (-want +got):\n%s", diff)
			}
			if _, err := os.Lstat(filepath.Join(parent, "escape.md")); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("Write() reached outside the root: Lstat error = %v", err)
			}
		})
	}
}

func TestWriteMode(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if _, _, err := Write(root, []File{{Path: "secret.txt", Content: []byte("s\n"), Mode: 0o600}}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	info, err := os.Stat(filepath.Join(root, "secret.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %v, want %v", got, fs.FileMode(0o600))
	}
}

func TestWriteRootMissing(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "absent")
	written, skipped, err := Write(root, []File{{Path: "README.md", Content: []byte("# x\n"), Mode: 0o644}})
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Write() error = %v, want fs.ErrNotExist", err)
	}
	if written != nil || skipped != nil {
		t.Errorf("Write() = %q, %q, want nil, nil", written, skipped)
	}
}

// TestSubstituteOverlap pins the behavior for rules whose matches overlap,
// which the rules of this package never produce: the earlier match wins.
func TestSubstituteOverlap(t *testing.T) {
	t.Parallel()

	rs := []rule{
		{name: "AB", re: regexp.MustCompile(`(AB)`), value: func(*Vars) string { return "1" }, required: true},
		{name: "BC", re: regexp.MustCompile(`(BC)`), value: func(*Vars) string { return "2" }, required: true},
		{name: "XY", re: regexp.MustCompile(`(XY)`), value: func(*Vars) string { return "3" }, required: true},
		{name: "ZZ", re: regexp.MustCompile(`(ZZ)`), value: func(*Vars) string { return "4" }},
	}
	got, unmatched := substitute([]byte("ABC ABC"), rs, &Vars{})
	if diff := gocmp.Diff("1C 1C", string(got)); diff != "" {
		t.Errorf("substitute() content mismatch (-want +got):\n%s", diff)
	}
	if diff := gocmp.Diff([]string{"XY"}, unmatched); diff != "" {
		t.Errorf("substitute() unmatched mismatch (-want +got):\n%s", diff)
	}
}
