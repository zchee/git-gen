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

// Package gitignore composes .gitignore and .gitattributes files from the
// templates of a github/gitignore checkout.
package gitignore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Scaffold is a set of files that a language adds to the generated repository.
type Scaffold uint8

const (
	// ScaffoldGo adds .github/, .golangci.yaml, hack/ (Apache-2.0 only), go.mod and go.sum.
	ScaffoldGo Scaffold = 1 << iota
	// ScaffoldMakefile adds the Makefile.
	ScaffoldMakefile
	// ScaffoldGoSumAttributes adds the go.sum block of .gitattributes.
	ScaffoldGoSumAttributes
)

// Language is a language argument resolved against a [Catalog].
type Language struct {
	// Arg is the argument as given on the command line.
	Arg string
	// Template is the resolved template name printed in the section header,
	// for example "Go", "community/Golang" or "Python".
	Template string
	// Scaffold is the set of files the language adds besides .gitignore.
	Scaffold Scaffold
}

// InvalidNameError reports a language name that is empty, absolute, or
// contains a ".." element and so could leave the catalog.
type InvalidNameError struct{ Name string }

// Error implements error.
func (e *InvalidNameError) Error() string {
	return fmt.Sprintf("gitignore: invalid template name %q", e.Name)
}

// Catalog is a github/gitignore checkout opened for reading. Every read goes
// through an [os.Root], so nothing outside the checkout is ever read.
type Catalog struct {
	root *os.Root
	fsys fs.FS
}

// Open opens the github/gitignore checkout at dir.
func Open(dir string) (*Catalog, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("gitignore: open catalog: %w", err)
	}
	return &Catalog{root: root, fsys: root.FS()}, nil
}

// Close releases the directory handle of the catalog.
func (c *Catalog) Close() error {
	if err := c.root.Close(); err != nil {
		return fmt.Errorf("gitignore: close catalog: %w", err)
	}
	return nil
}

// listDepth is the depth limit of the listing, as `find -maxdepth 3`.
const listDepth = 3

// List returns the template names of the catalog: the paths of the regular
// *.gitignore files at most three levels deep, relative to the catalog root,
// cut to at most two elements, without the extension and without duplicates,
// sorted by bytes. Symbolic links are not listed.
func (c *Catalog) List() ([]string, error) {
	var names []string
	err := fs.WalkDir(c.fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		if d.IsDir() {
			if strings.Count(p, "/")+1 >= listDepth {
				return fs.SkipDir
			}
			return nil
		}
		// A file named exactly ".gitignore" is the checkout's own ignore file, not a template.
		if !d.Type().IsRegular() || d.Name() == ".gitignore" || !strings.HasSuffix(d.Name(), ".gitignore") {
			return nil
		}
		name := p
		if first, rest, ok := strings.Cut(p, "/"); ok {
			second, _, _ := strings.Cut(rest, "/")
			name = first + "/" + second
		}
		names = append(names, strings.TrimSuffix(name, ".gitignore"))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("gitignore: list catalog: %w", err)
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}

// Resolve resolves language arguments to templates, keeping the order of args.
//
// An argument in the language table maps to its fixed template. Any other
// argument resolves to "<arg>.gitignore", or else to the single
// "*.AllowList.gitignore" directly inside the directory "<arg>". Every path
// element must match a catalog entry exactly, so the result is the same on
// case-sensitive and case-insensitive file systems.
//
// A well-formed argument that matches no template is returned in unknown. An
// empty or absolute argument, or one with a ".." element, fails with
// [*InvalidNameError].
func (c *Catalog) Resolve(args []string) (langs []Language, unknown []string, err error) {
	for _, arg := range args {
		s, ok, err := c.lookup(arg)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			unknown = append(unknown, arg)
			continue
		}
		langs = append(langs, Language{Arg: arg, Template: s.template, Scaffold: s.rule.scaffold})
	}
	return langs, unknown, nil
}

// section is a resolved template: the name printed in its header, the file
// that holds it and the rule that shapes its body.
type section struct {
	template string
	file     string
	rule     rule
}

// lookup resolves one language argument.
func (c *Catalog) lookup(name string) (section, bool, error) {
	if !validName(name) {
		return section{}, false, &InvalidNameError{Name: name}
	}
	if r, ok := rules[name]; ok {
		s := section{template: r.template, file: r.file, rule: r}
		if r.file == "" {
			file, found, err := c.allowList(r.template)
			s.file = file
			return s, found, err
		}
		found, err := c.isFile(r.file)
		return s, found, err
	}
	s := section{template: name, file: name + ".gitignore"}
	if found, err := c.isFile(s.file); err != nil || found {
		return s, found, err
	}
	file, found, err := c.allowList(name)
	s.file = file
	return s, found, err
}

// validName reports whether name is non-empty, relative and free of ".."
// elements.
func validName(name string) bool {
	if !filepath.IsLocal(name) {
		return false
	}
	for elem := range strings.SplitSeq(filepath.ToSlash(name), "/") {
		if elem == ".." {
			return false
		}
	}
	return true
}

// entry returns the catalog entry at the slash-separated path rel; found is
// false when there is none. Every element must equal a directory entry name
// byte for byte, and every element but the last must be a real directory, not
// a symbolic link.
func (c *Catalog) entry(rel string) (d fs.DirEntry, found bool, err error) {
	dir := "."
	for elem := range strings.SplitSeq(rel, "/") {
		if d != nil && !d.IsDir() {
			return nil, false, nil
		}
		var entries []fs.DirEntry
		if entries, err = fs.ReadDir(c.fsys, dir); err != nil {
			return nil, false, fmt.Errorf("gitignore: read %s: %w", dir, err)
		}
		i := slices.IndexFunc(entries, func(e fs.DirEntry) bool { return e.Name() == elem })
		if i < 0 {
			return nil, false, nil
		}
		d = entries[i]
		dir = path.Join(dir, elem)
	}
	return d, true, nil
}

// isFile reports whether rel names a template file in the catalog.
func (c *Catalog) isFile(rel string) (bool, error) {
	d, found, err := c.entry(rel)
	if err != nil || !found {
		return false, err
	}
	return c.isFileEntry(rel, d)
}

// isFileEntry reports whether the entry e at rel is a regular file, or a
// symbolic link to a regular file that stays inside the catalog. A link that
// leaves the catalog is an error, never a read.
func (c *Catalog) isFileEntry(rel string, e fs.DirEntry) (bool, error) {
	switch {
	case e.Type().IsRegular():
		return true, nil
	case e.Type()&fs.ModeSymlink == 0:
		return false, nil
	}
	info, err := fs.Stat(c.fsys, rel)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("gitignore: %w", err)
	}
	return info.Mode().IsRegular(), nil
}

// allowListSuffix is the file name suffix of an allowlist template.
const allowListSuffix = ".AllowList.gitignore"

// allowList returns the single allowlist template directly inside dir.
func (c *Catalog) allowList(dir string) (file string, found bool, err error) {
	d, isEntry, err := c.entry(dir)
	if err != nil || !isEntry || !d.IsDir() {
		return "", false, err
	}
	entries, err := fs.ReadDir(c.fsys, dir)
	if err != nil {
		return "", false, fmt.Errorf("gitignore: read %s: %w", dir, err)
	}
	var matches []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), allowListSuffix) {
			continue
		}
		rel := path.Join(dir, e.Name())
		var regular bool
		if regular, err = c.isFileEntry(rel, e); err != nil {
			return "", false, err
		}
		if regular {
			matches = append(matches, rel)
		}
	}
	switch len(matches) {
	case 0:
		return "", false, nil
	case 1:
		return matches[0], true, nil
	}
	return "", false, fmt.Errorf("gitignore: %s holds %d allowlist templates (%s); want one", dir, len(matches), strings.Join(matches, ", "))
}
