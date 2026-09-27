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
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/go-git/go-git/v6/plumbing/format/gitignore"
)

// ignoreMatcher tells whether git would ignore a path, from the .gitignore files of one working tree.
// Global excludes and .git/info/exclude are not read.
//
// It does not use gitignore.Matcher: that matcher tries a pattern without a slash against every component
// of a path, so the "!*/" of an allowlist template re-includes every file below a directory, where git
// ignores them. It follows git instead: each leading directory is checked on its own level, a pattern
// without a slash is matched against the last component only, and a directory that is ignored makes
// everything below it ignored. gitignore.ParsePattern supplies the wildcard matching of one component.
type ignoreMatcher struct {
	root  string
	rules map[string][]ignoreRule // by slash-separated directory, "" for the root
}

// ignoreRule is one pattern line of a .gitignore file.
type ignoreRule struct {
	domain   []string // directory of the .gitignore file
	segments []ignoreSegment
	anchored bool // the pattern had a slash before its end, so it is relative to domain
	dirOnly  bool
	negate   bool
}

// ignoreSegment is one slash-separated part of a pattern.
type ignoreSegment struct {
	glob    string
	pattern gitignore.Pattern
}

func newIgnoreMatcher(root string) *ignoreMatcher {
	return &ignoreMatcher{root: root, rules: make(map[string][]ignoreRule)}
}

// ignored reports whether the slash-separated path p is ignored. isDir tells whether p is a directory.
func (m *ignoreMatcher) ignored(p string, isDir bool) (bool, error) {
	parts := strings.Split(p, "/")
	var rules []ignoreRule
	for n := 1; n <= len(parts); n++ {
		dirRules, err := m.load(parts[:n-1])
		if err != nil {
			return false, err
		}
		rules = append(rules, dirRules...)

		candidate, candidateIsDir := parts[:n], n < len(parts) || isDir
		for _, rule := range slices.Backward(rules) {
			if rule.match(candidate, candidateIsDir) {
				if !rule.negate {
					return true, nil
				}
				break
			}
		}
	}
	return false, nil
}

// load returns the rules of the .gitignore file in dir, in file order.
func (m *ignoreMatcher) load(dir []string) ([]ignoreRule, error) {
	key := strings.Join(dir, "/")
	if rules, ok := m.rules[key]; ok {
		return rules, nil
	}

	var rules []ignoreRule
	data, err := os.ReadFile(filepath.Join(m.root, filepath.FromSlash(key), ".gitignore"))
	switch {
	case errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR):
	case err != nil:
		return nil, fmt.Errorf("read .gitignore: %w", err)
	default:
		for line := range strings.SplitSeq(string(data), "\n") {
			if rule, ok := parseIgnoreLine(line, slices.Clone(dir)); ok {
				rules = append(rules, rule)
			}
		}
	}
	m.rules[key] = rules
	return rules, nil
}

// parseIgnoreLine parses one line of a .gitignore file in directory domain. It reports false for a blank
// line or a comment.
func parseIgnoreLine(line string, domain []string) (ignoreRule, bool) {
	if strings.HasPrefix(line, "#") {
		return ignoreRule{}, false
	}
	line = trimTrailingSpaces(line)

	rule := ignoreRule{domain: domain}
	line, rule.negate = strings.CutPrefix(line, "!")
	line, rule.dirOnly = strings.CutSuffix(line, "/")
	if line == "" {
		return ignoreRule{}, false
	}
	if strings.Contains(line, "/") {
		rule.anchored = true
		line = strings.TrimPrefix(line, "/")
	}
	for seg := range strings.SplitSeq(line, "/") {
		rule.segments = append(rule.segments, newIgnoreSegment(seg))
	}
	return rule, true
}

// trimTrailingSpaces removes the trailing spaces that are not escaped with a backslash, as git's
// trim_trailing_spaces does.
func trimTrailingSpaces(s string) string {
	lastSpace := -1
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ':
			if lastSpace < 0 {
				lastSpace = i
			}
		case '\\':
			i++
			if i == len(s) {
				return s
			}
			lastSpace = -1
		default:
			lastSpace = -1
		}
	}
	if lastSpace >= 0 {
		return s[:lastSpace]
	}
	return s
}

// newIgnoreSegment escapes what gitignore.ParsePattern would otherwise read as syntax of a whole pattern:
// a leading "!" and a trailing space.
func newIgnoreSegment(seg string) ignoreSegment {
	glob := seg
	if strings.HasPrefix(glob, "!") {
		glob = `\` + glob
	}
	if strings.HasSuffix(glob, " ") && !strings.HasSuffix(glob, `\ `) {
		glob = glob[:len(glob)-1] + `\ `
	}
	return ignoreSegment{glob: seg, pattern: gitignore.ParsePattern(glob, nil)}
}

func (s ignoreSegment) match(name string) bool {
	return s.pattern.Match([]string{name}, false) != gitignore.NoMatch
}

// match reports whether the rule matches path, whose last component is a directory when isDir is true.
// path must lie below r.domain, which ignored guarantees by applying only the rules of leading directories.
func (r ignoreRule) match(path []string, isDir bool) bool {
	if r.dirOnly && !isDir {
		return false
	}
	rel := path[len(r.domain):]
	if !r.anchored {
		return r.segments[0].match(rel[len(rel)-1])
	}
	return matchSegments(r.segments, rel)
}

// matchSegments matches an anchored pattern against a whole path. A "**" segment matches any number of
// components; a trailing "**" matches at least one.
func matchSegments(segs []ignoreSegment, path []string) bool {
	for len(segs) > 0 {
		if segs[0].glob == "**" {
			if len(segs) == 1 {
				return len(path) > 0
			}
			for i := range len(path) + 1 {
				if matchSegments(segs[1:], path[i:]) {
					return true
				}
			}
			return false
		}
		if len(path) == 0 || !segs[0].match(path[0]) {
			return false
		}
		segs, path = segs[1:], path[1:]
	}
	return len(path) == 0
}
