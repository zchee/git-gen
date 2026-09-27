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
	"slices"
)

// attributesHeader follows "# <author>" on the first line of the
// .gitattributes header block.
const attributesHeader = ` project gitattributes file
#  https://github.com/github-linguist/linguist/blob/main/docs/overrides.md

* text=auto eol=lf
`

// goSumAttributes is the go.sum block of .gitattributes. It is appended after
// a blank line.
const goSumAttributes = "go.sum       linguist-vendored\ngo.work.sum  linguist-vendored\n"

// Attributes returns the content of .gitattributes: existing followed by the
// header block that names author and, when any of langs has
// [ScaffoldGoSumAttributes], the go.sum block.
//
// Each block is appended only when it is not already present as whole lines
// of existing, and the two blocks are checked separately, so applying
// Attributes to its own output returns it unchanged. A header block that
// names another author counts as absent. When a block is appended to content
// that does not end with a newline, a newline is added first. existing is
// never modified.
func Attributes(existing []byte, author string, langs []Language) []byte {
	base := existing
	if len(base) > 0 && base[len(base)-1] != '\n' {
		base = append(slices.Clip(base), '\n')
	}
	var add []byte
	if header := "# " + author + attributesHeader; !hasBlock(base, header) {
		add = append(add, header...)
	}
	if Scaffolds(langs)&ScaffoldGoSumAttributes != 0 && !hasBlock(base, goSumAttributes) {
		add = append(add, '\n')
		add = append(add, goSumAttributes...)
	}
	if len(add) == 0 {
		return bytes.Clone(existing)
	}
	return slices.Concat(base, add)
}

// hasBlock reports whether block, which ends with a newline, occurs in data
// starting at the beginning of a line.
func hasBlock(data []byte, block string) bool {
	return bytes.HasPrefix(data, []byte(block)) || bytes.Contains(data, []byte("\n"+block))
}

// Scaffolds returns the union of the scaffolds of langs.
func Scaffolds(langs []Language) Scaffold {
	var s Scaffold
	for _, lang := range langs {
		s |= lang.Scaffold
	}
	return s
}
