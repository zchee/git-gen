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

// Package boilerplate renders the files of the owner's boilerplate directory
// for a new repository, writes them without overwriting, and creates go.mod.
package boilerplate

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Vars holds the values substituted into the templates.
type Vars struct {
	Author       string
	Organization string
	Project      string
	Contact      string
	SPDXID       string
	Year         int
}

// Set selects the optional groups of files that [Render] returns.
type Set struct {
	Go       bool // .github/ templates and .golangci.yaml
	Makefile bool
	Hack     bool // hack/boilerplate/boilerplate.go.txt
}

// File is a file to place. Path is slash-separated and relative to the
// repository root; Mode holds the permission bits of the template.
type File struct {
	Path    string
	Content []byte
	Mode    fs.FileMode
}

// Template paths, slash-separated and relative to the boilerplate directory.
const (
	githubDir     = ".github"
	codeOfConduct = ".github/CODE_OF_CONDUCT.md"
	bugReport     = ".github/ISSUE_TEMPLATE/bug_report.yml"
	golangci      = "go/.golangci.yaml"
	makefile      = "go/Makefile"
	hackHeader    = "go/boilerplate.go.txt"
)

// rule replaces the first capture group of every match of re with the value
// computed from [Vars]. A required rule that matches nothing makes [Render]
// warn, so that a change in the template is noticed instead of shipping the
// placeholder.
type rule struct {
	name     string
	re       *regexp.Regexp
	value    func(*Vars) string
	required bool
}

// wordRule matches token only where it stands alone as a word: AUTHOR matches
// "The AUTHOR Authors" but not "AUTHORS" or "_AUTHOR".
func wordRule(token string, value func(*Vars) string) rule {
	return rule{
		name:     token,
		re:       regexp.MustCompile(`\b(` + regexp.QuoteMeta(token) + `)\b`),
		value:    value,
		required: true,
	}
}

func modulePrefix(v *Vars) string {
	return "github.com/" + v.Organization + "/" + v.Project
}

// rules maps a template path to the substitutions made in it. Rules are per
// file: the {{ YEAR }} comment of .golangci.yaml is not a boilerplate.go.txt
// token and passes through untouched.
var rules = map[string][]rule{
	codeOfConduct: {{
		name:     "[INSERT CONTACT METHOD]",
		re:       regexp.MustCompile(`(\[INSERT CONTACT METHOD\])`),
		value:    func(v *Vars) string { return v.Contact },
		required: true,
	}},
	bugReport: {
		wordRule("PKG", func(v *Vars) string { return v.Project }),
	},
	golangci: {
		{
			name:     "gci prefix(github.com/)",
			re:       regexp.MustCompile(`prefix\((github\.com/)\)`),
			value:    modulePrefix,
			required: true,
		},
		{
			name:     "gofumpt module-path: github.com/",
			re:       regexp.MustCompile(`(?m)^[ \t]*module-path:[ \t]*(github\.com/)[ \t]*\r?$`),
			value:    modulePrefix,
			required: true,
		},
		{
			// The item may follow other items of the same local-prefixes list.
			name:     "goimports local-prefixes item - github.com/",
			re:       regexp.MustCompile(`(?m)^[ \t]*local-prefixes:[ \t]*\r?\n(?:[ \t]*-[^\n]*\n)*?[ \t]*-[ \t]*(github\.com/)[ \t]*\r?$`),
			value:    modulePrefix,
			required: true,
		},
	},
	hackHeader: {
		wordRule("AUTHOR", func(v *Vars) string { return v.Author }),
		wordRule("YEAR", func(v *Vars) string { return strconv.Itoa(v.Year) }),
		{
			name:  "LICENSE_IDENTIFIER",
			re:    regexp.MustCompile(`(LICENSE_IDENTIFIER)`),
			value: func(v *Vars) string { return v.SPDXID },
		},
	},
}

// Dir returns the boilerplate directory: $XDG_CONFIG_HOME/boilerplate, or
// <home>/.config/boilerplate when XDG_CONFIG_HOME is empty. It does not use
// [os.UserConfigDir], which returns a different directory on macOS.
func Dir(getenv func(string) string, home string) string {
	if xdg := getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "boilerplate")
	}

	return filepath.Join(home, ".config", "boilerplate")
}

// templates returns the templates that [Render] needs for set. Render walks
// .github/ for the rest of its files, so of that directory only the files that
// Render reads by name or that carry a required rule are listed.
func templates(set Set) []string {
	srcs := []string{codeOfConduct}
	if set.Go {
		srcs = append(srcs, bugReport, golangci)
	}
	if set.Makefile {
		srcs = append(srcs, makefile)
	}
	if set.Hack {
		srcs = append(srcs, hackHeader)
	}

	return srcs
}

// Missing returns, as absolute paths, the templates that [Render] needs for set
// and that do not exist as regular files under dir. It returns nil when none
// is missing.
func Missing(dir string, set Set) []string {
	var missing []string
	for _, src := range templates(set) {
		name := filepath.Join(dir, filepath.FromSlash(src))
		if info, err := os.Stat(name); err == nil && info.Mode().IsRegular() {
			continue
		}
		if abs, err := filepath.Abs(name); err == nil {
			name = abs
		}
		missing = append(missing, name)
	}

	return missing
}

// Render returns the files to place for set, sorted by path, with the
// placeholders replaced by the values of v.
//
// CODE_OF_CONDUCT.md (from .github/CODE_OF_CONDUCT.md) and README.md are always
// returned. With set.Go, every regular file under .github/ is returned except
// .github/CODE_OF_CONDUCT.md and any .DS_Store; symbolic links to regular files
// are followed and anything else is skipped. Each warning names a required
// rule that matched nothing and the template it belongs to.
//
//nolint:gocritic // hugeParam: Render runs once per process; Vars stays a value so callers build it inline.
func Render(dir string, set Set, v Vars) (files []File, warnings []string, err error) {
	fsys := os.DirFS(dir)
	add := func(src, dst string) error {
		file, warns, err := load(fsys, dir, src, dst, &v)
		if err != nil {
			return err
		}
		files = append(files, file)
		warnings = append(warnings, warns...)

		return nil
	}

	if err := add(codeOfConduct, "CODE_OF_CONDUCT.md"); err != nil {
		return nil, nil, err
	}
	files = append(files, File{Path: "README.md", Content: fmt.Appendf(nil, "# %s\n", v.Project), Mode: 0o644})

	if set.Go {
		srcs, err := githubTemplates(fsys, dir)
		if err != nil {
			return nil, nil, err
		}
		for _, src := range srcs {
			if err := add(src, src); err != nil {
				return nil, nil, err
			}
		}
		if err := add(golangci, ".golangci.yaml"); err != nil {
			return nil, nil, err
		}
	}
	if set.Makefile {
		if err := add(makefile, "Makefile"); err != nil {
			return nil, nil, err
		}
	}
	if set.Hack {
		if err := add(hackHeader, "hack/boilerplate/boilerplate.go.txt"); err != nil {
			return nil, nil, err
		}
	}

	slices.SortFunc(files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })

	return files, warnings, nil
}

// githubTemplates returns the regular files under .github/ except
// .github/CODE_OF_CONDUCT.md, which is placed at the top level, and any
// .DS_Store. Symbolic links are followed; anything that is not a regular file
// once they are is left out, and a dangling link is an error.
func githubTemplates(fsys fs.FS, dir string) ([]string, error) {
	var entries []string
	err := fs.WalkDir(fsys, githubDir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && p != codeOfConduct && path.Base(p) != ".DS_Store" {
			entries = append(entries, p)
		}

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("boilerplate: walk %s: %w", filepath.Join(dir, githubDir), err)
	}

	srcs := entries[:0]
	for _, p := range entries {
		info, err := fs.Stat(fsys, p)
		if err != nil {
			return nil, fmt.Errorf("boilerplate: template %s: %w", filepath.Join(dir, filepath.FromSlash(p)), err)
		}
		if info.Mode().IsRegular() {
			srcs = append(srcs, p)
		}
	}

	return srcs, nil
}

// load reads the template src, applies its rules and returns it as dst.
func load(fsys fs.FS, dir, src, dst string, v *Vars) (File, []string, error) {
	name := filepath.Join(dir, filepath.FromSlash(src))
	info, err := fs.Stat(fsys, src)
	if err != nil {
		return File{}, nil, fmt.Errorf("boilerplate: template %s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return File{}, nil, fmt.Errorf("boilerplate: template %s: not a regular file", name)
	}
	content, err := fs.ReadFile(fsys, src)
	if err != nil {
		return File{}, nil, fmt.Errorf("boilerplate: template %s: %w", name, err)
	}

	content, unmatched := substitute(content, rules[src], v)
	warnings := make([]string, 0, len(unmatched))
	for _, ruleName := range unmatched {
		warnings = append(warnings, fmt.Sprintf("template %s: required rule %q matched nothing", name, ruleName))
	}

	return File{Path: dst, Content: content, Mode: info.Mode().Perm()}, warnings, nil
}

// substitute applies rs to content in a single pass over the template, so a
// value inserted by one rule is never rewritten by another. Values are inserted
// literally. It returns the names of the required rules that matched nothing.
func substitute(content []byte, rs []rule, v *Vars) (out []byte, unmatched []string) {
	type edit struct {
		start, end int
		value      string
	}

	var edits []edit
	for _, r := range rs {
		matches := r.re.FindAllSubmatchIndex(content, -1)
		if len(matches) == 0 && r.required {
			unmatched = append(unmatched, r.name)
		}
		value := r.value(v)
		for _, m := range matches {
			edits = append(edits, edit{start: m[2], end: m[3], value: value})
		}
	}
	slices.SortStableFunc(edits, func(a, b edit) int { return cmp.Compare(a.start, b.start) })

	out = make([]byte, 0, len(content))
	last := 0
	for _, e := range edits {
		if e.start < last {
			continue // overlaps the preceding edit, which wins
		}
		out = append(out, content[last:e.start]...)
		out = append(out, e.value...)
		last = e.end
	}
	out = append(out, content[last:]...)

	return out, unmatched
}

// Write creates files under root, creating parent directories as needed. It
// never overwrites: a path that already exists, as anything, is left untouched
// and reported in skipped. It creates no directory without also creating a
// file in it, and it cannot write outside root. written and skipped hold the
// Path of each file in the order given.
func Write(root string, files []File) (written, skipped []string, err error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, nil, fmt.Errorf("boilerplate: %w", err)
	}
	defer r.Close()

	for _, file := range files {
		created, err := create(r, file)
		if err != nil {
			return written, skipped, err
		}
		if created {
			written = append(written, file.Path)
		} else {
			skipped = append(skipped, file.Path)
		}
	}

	return written, skipped, nil
}

// create writes file under r and reports false when its path already exists.
func create(r *os.Root, file File) (bool, error) {
	fail := func(err error) (bool, error) {
		return false, fmt.Errorf("boilerplate: write %s: %w", file.Path, err)
	}

	name := filepath.FromSlash(file.Path)
	if _, err := r.Lstat(name); err == nil {
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fail(err)
	}

	if parent := filepath.Dir(name); parent != "." {
		if err := r.MkdirAll(parent, 0o755); err != nil {
			return fail(err)
		}
	}
	f, err := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, file.Mode.Perm())
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return fail(err)
	}

	// A partial file would be skipped as existing by every later run.
	if _, err := f.Write(file.Content); err != nil {
		return fail(errors.Join(err, f.Close(), r.Remove(name)))
	}
	if err := f.Close(); err != nil {
		return fail(errors.Join(err, r.Remove(name)))
	}

	return true, nil
}
