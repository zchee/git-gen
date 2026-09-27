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
	"fmt"
	"io/fs"
)

// rule is the treatment of one language of the language table.
type rule struct {
	// template is the name printed in the section header.
	template string
	// file is the template file relative to the catalog root. Empty means
	// the single allowlist template inside the directory template.
	file string
	// trim removes lines from the top of the template.
	trim func([]byte) []byte
	// edit rewrites the section body, which ends with one newline, and
	// returns a description of every edit that found nothing to change.
	edit func([]byte) ([]byte, []string)
	// suffix is appended after the section.
	suffix string
	// scaffold is the set of files the language adds.
	scaffold Scaffold
}

// goFile is the template behind "go", "Go", "go-pkg" and "go-simple".
const goFile = "Go.gitignore"

// rules is the language table. An argument that is not a key resolves by
// name and keeps its template as it is.
var rules = map[string]rule{
	"go": {
		template: "Go",
		file:     goFile,
		trim:     dropLines(2),
		edit:     applyEdits(goEdits),
		suffix:   goSuffix,
		scaffold: ScaffoldGo | ScaffoldGoSumAttributes,
	},
	"Go": {
		template: "Go",
		file:     goFile,
		trim:     dropLines(2),
		edit:     applyEdits(goEdits),
		suffix:   goSuffix,
		scaffold: ScaffoldGo | ScaffoldGoSumAttributes | ScaffoldMakefile,
	},
	"go-pkg":    {template: "Go", file: goFile, trim: dropLines(2)},
	"go-simple": {template: "Go", file: goFile, trim: dropLines(2)},
	"Rust":      {template: "Rust", file: "Rust.gitignore", trim: dropLines(2), edit: dropRustRover},
	"community/Golang": {
		template: "community/Golang",
		trim:     dropLeadingComment,
		edit:     applyEdits(golangEdits),
		scaffold: ScaffoldGo | ScaffoldMakefile,
	},
}

// replacement replaces literal text in a section body.
type replacement struct {
	// what describes the edit in a warning.
	what string
	old  string
	new  string
	// every replaces every occurrence, as a sed substitution applied to each
	// line; otherwise only the first occurrence is replaced.
	every bool
}

// goEdits are the edits of Go.gitignore for "go" and "Go".
var goEdits = []replacement{
	{
		what: "uncomment vendor/",
		old:  "# Dependency directories (remove the comment below to include it)\n# vendor/",
		new:  "# Dependency directories\nvendor/",
	},
	{what: "remove the go.work block", old: "# Go workspace file\ngo.work\ngo.work.sum\n\n"},
	{what: "remove the .env block", old: "\n\n# env file\n.env"},
}

// golangEdits are the edits of community/Golang/Go.AllowList.gitignore. The
// "# Recommended: Go.AllowList.gitignore" line needs no edit: it belongs to the
// leading comment block, which is dropped before the edits run.
var golangEdits = []replacement{
	{what: "allow /.gitattributes", old: "# But not these files...", new: "!/.gitattributes", every: true},
	{what: "allow Makefile", old: "# !Makefile", new: "!Makefile", every: true},
	{what: "remove the subdirectories comment", old: "# ...even if they are in subdirectories\n"},
}

// goSuffix is the block that "go" and "Go" append after the Go section.
const goSuffix = `
# Compiled Object files, Static and Dynamic libs (Shared Objects)
*.o
*.a

# Folders
_obj
_test

# Architecture specific extensions/prefixes
*.[568vq]
[568vq].out

# cgo generated
*.cgo1.go
*.cgo2.c
_cgo_defun.c
_cgo_gotypes.go
_cgo_export.*

# test generated
_testmain.go

# benchmark
old.txt
new.txt
bench.txt

# profile
*.pprof
`

// ignoreHeader follows "# <author>" on the first line of .gitignore.
const ignoreHeader = ` project generated files to ignore
#  If you want to ignore files created by your editor/tools,
#  please consider a global .gitignore https://docs.github.com/en/get-started/git-basics/ignoring-files.
#  PLEASE DO NOT open a pull request to add something created by your editor or tools
`

// Compose returns the content of .gitignore for langs.
//
// The content is a header that names author, followed by one section per
// distinct template in first-seen order: a blank line, "# github/gitignore/<template>",
// and the template body without its trailing newlines, then one newline. The
// author appears only in the header. When several languages share a template,
// the section applies the edits of the first of them that has any, so
// "go-pkg go" gives the edited Go section.
//
// Each Language is resolved again by its Arg, so langs must come from
// [Catalog.Resolve]. An edit that finds nothing to change leaves the section
// as it is and adds a warning.
func (c *Catalog) Compose(author string, langs []Language) (content []byte, warnings []string, err error) {
	var sections []section
	index := make(map[string]int, len(langs))
	for _, lang := range langs {
		s, ok, err := c.lookup(lang.Arg)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return nil, nil, fmt.Errorf("gitignore: no template for %q", lang.Arg)
		}
		if i, seen := index[s.template]; seen {
			if sections[i].rule.edit == nil && s.rule.edit != nil {
				sections[i].rule = s.rule
			}
			continue
		}
		index[s.template] = len(sections)
		sections = append(sections, s)
	}

	content = fmt.Appendf(nil, "# %s%s", author, ignoreHeader)
	for _, s := range sections {
		body, err := fs.ReadFile(c.fsys, s.file)
		if err != nil {
			return nil, nil, fmt.Errorf("gitignore: read template %s: %w", s.template, err)
		}
		if s.rule.trim != nil {
			body = s.rule.trim(body)
		}
		// The script prints the body with its trailing newlines stripped and
		// then one newline, and edits that file line by line afterwards.
		body = append(bytes.TrimRight(body, "\n"), '\n')
		if s.rule.edit != nil {
			var missed []string
			body, missed = s.rule.edit(body)
			for _, what := range missed {
				warnings = append(warnings, fmt.Sprintf("github/gitignore/%s: cannot %s: text not found, section left as it is", s.template, what))
			}
		}
		content = fmt.Appendf(content, "\n# github/gitignore/%s\n%s%s", s.template, body, s.rule.suffix)
	}
	return content, warnings, nil
}

// dropLines returns a trim that removes the first n lines.
func dropLines(n int) func([]byte) []byte {
	return func(b []byte) []byte {
		for range n {
			_, rest, ok := bytes.Cut(b, []byte("\n"))
			if !ok {
				return nil
			}
			b = rest
		}
		return b
	}
}

// dropLeadingComment removes the comment lines at the top of b and, when
// there were any, the blank line after them.
func dropLeadingComment(b []byte) []byte {
	dropped := false
	for bytes.HasPrefix(b, []byte("#")) {
		_, rest, ok := bytes.Cut(b, []byte("\n"))
		if !ok {
			return nil
		}
		b, dropped = rest, true
	}
	if rest, ok := bytes.CutPrefix(b, []byte("\n")); ok && dropped {
		b = rest
	}
	return b
}

// applyEdits returns an edit that applies edits in order.
func applyEdits(edits []replacement) func([]byte) ([]byte, []string) {
	return func(body []byte) ([]byte, []string) {
		var missed []string
		for _, e := range edits {
			old := []byte(e.old)
			if !bytes.Contains(body, old) {
				missed = append(missed, e.what)
				continue
			}
			n := 1
			if e.every {
				n = -1
			}
			body = bytes.Replace(body, old, []byte(e.new), n)
		}
		return body, missed
	}
}

// dropRustRover removes the block from the line "# RustRover" through the
// line "#.idea/" and the blank line before it.
func dropRustRover(body []byte) (out []byte, missed []string) {
	const first, last = "# RustRover", "#.idea/"
	start := findLine(body, first, 0)
	if start < 0 {
		return body, []string{"remove the RustRover block"}
	}
	end := findLine(body, last, start)
	if end < 0 {
		return body, []string{"remove the RustRover block"}
	}
	end = min(end+len(last)+1, len(body))
	if start > 0 && (start == 1 || body[start-2] == '\n') {
		start--
	}
	return append(body[:start:start], body[end:]...), nil
}

// findLine returns the offset of the first line of b that equals line and
// starts at or after the line start from, or -1.
func findLine(b []byte, line string, from int) int {
	for i := from; i < len(b); {
		n := bytes.IndexByte(b[i:], '\n')
		if n < 0 {
			n = len(b) - i
		}
		if string(b[i:i+n]) == line {
			return i
		}
		i += n + 1
	}
	return -1
}
