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

// Package license resolves license names and renders LICENSE files from the embedded SPDX License List
// texts.
package license

import (
	"bytes"
	"embed"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/go-json-experiment/json"
)

// The IDs passed to spdxgen must be the SPDX IDs of table; TestManifestIDs enforces it.
//go:generate go run ./spdxgen MIT BSD-3-Clause Apache-2.0 CC-BY-SA-4.0

//go:embed data/*.txt data/manifest.json
var dataFS embed.FS

// Holder selects who the copyright line of a rendered license names.
type Holder int

const (
	// HolderNone leaves the license text as published.
	HolderNone Holder = iota
	// HolderProject names "The <author> Authors".
	HolderProject
	// HolderOwner names the identity name.
	HolderOwner
	// HolderGoAuthors names "The Go Authors".
	HolderGoAuthors
)

// License is one row of the license table.
type License struct {
	// Name is the canonical command-line name: the first alias of the row.
	Name string
	// SPDXID is empty for "none".
	SPDXID string
	Holder Holder
}

// Vars are the values a copyright line is filled with.
type Vars struct {
	Author string
	Owner  string
	Year   int
}

// SPDX IDs of the table rows.
const (
	idBSD3Clause = "BSD-3-Clause"
	idMIT        = "MIT"
	idApache20   = "Apache-2.0"
	idCCBYSA40   = "CC-BY-SA-4.0"
)

var table = []struct {
	names  []string
	spdxID string
	holder Holder
}{
	{names: []string{"bsd", "bsd-project"}, spdxID: idBSD3Clause, holder: HolderProject},
	{names: []string{"bsd-owner"}, spdxID: idBSD3Clause, holder: HolderOwner},
	{names: []string{"bsd-go"}, spdxID: idBSD3Clause, holder: HolderGoAuthors},
	{names: []string{"mit", "mit-project"}, spdxID: idMIT, holder: HolderProject},
	{names: []string{"mit-owner"}, spdxID: idMIT, holder: HolderOwner},
	{names: []string{"apache2", "Apache2"}, spdxID: idApache20, holder: HolderNone},
	{names: []string{"CC4", "CC-BY-SA-4.0"}, spdxID: idCCBYSA40, holder: HolderNone},
	{names: []string{"none"}},
}

// Lookup returns the license accepted under name. Names are case-sensitive.
func Lookup(name string) (License, bool) {
	for _, row := range table {
		if slices.Contains(row.names, name) {
			return License{Name: row.names[0], SPDXID: row.spdxID, Holder: row.holder}, true
		}
	}
	return License{}, false
}

// Names returns every accepted name in table order.
func Names() []string {
	var names []string
	for _, row := range table {
		names = append(names, row.names...)
	}
	return names
}

// ListVersion returns the version of the SPDX License List the embedded texts come from.
func ListVersion() string {
	return embedded.LicenseListVersion
}

// Render returns the LICENSE file content. It returns nil, nil for "none". For a license whose Holder is
// not HolderNone, the line that holds the SPDX copyright placeholder is replaced as a whole by the filled
// copyright line. The result ends with exactly one newline.
func (l License) Render(v Vars) ([]byte, error) {
	if l.SPDXID == "" {
		return nil, nil
	}
	text, err := dataFS.ReadFile("data/" + l.SPDXID + ".txt")
	if err != nil {
		return nil, fmt.Errorf("license %s: %w", l.SPDXID, err)
	}
	if l.Holder == HolderNone {
		return withFinalNewline(text), nil
	}
	holder, err := l.holderName(v)
	if err != nil {
		return nil, err
	}
	if v.Year <= 0 {
		return nil, fmt.Errorf("license %s: year %d is not positive", l.SPDXID, v.Year)
	}
	out, err := fillCopyright(text, embedded.Licenses[l.SPDXID].Placeholder, holder, v.Year)
	if err != nil {
		return nil, fmt.Errorf("license %s: %w", l.SPDXID, err)
	}
	return out, nil
}

func (l License) holderName(v Vars) (string, error) {
	switch l.Holder {
	case HolderProject:
		if v.Author == "" {
			return "", fmt.Errorf("license %s: author is empty", l.SPDXID)
		}
		return "The " + v.Author + " Authors", nil
	case HolderOwner:
		if v.Owner == "" {
			return "", fmt.Errorf("license %s: owner is empty", l.SPDXID)
		}
		return v.Owner, nil
	case HolderGoAuthors:
		return "The Go Authors", nil
	default:
		return "", fmt.Errorf("license %s: unknown holder %d", l.SPDXID, l.Holder)
	}
}

// fillCopyright replaces the whole line of text that holds placeholder with the copyright line built from
// it, so whitespace the published line carries around the placeholder does not survive.
func fillCopyright(text []byte, placeholder, holder string, year int) ([]byte, error) {
	line, err := copyrightLine(placeholder, holder, year)
	if err != nil {
		return nil, err
	}
	p := []byte(placeholder)
	if n := bytes.Count(text, p); n != 1 {
		return nil, fmt.Errorf("copyright placeholder %q occurs %d times, want 1", placeholder, n)
	}
	i := bytes.Index(text, p)
	start := bytes.LastIndexByte(text[:i], '\n') + 1
	end := len(text)
	if j := bytes.IndexByte(text[i:], '\n'); j >= 0 {
		end = i + j
	}
	out := make([]byte, 0, len(text)-(end-start)+len(line)+1)
	out = append(out, text[:start]...)
	out = append(out, line...)
	out = append(out, text[end:]...)
	return withFinalNewline(out), nil
}

// copyrightLine substitutes the <year> token of placeholder with year and its one other <...> token with
// holder.
func copyrightLine(placeholder, holder string, year int) (string, error) {
	var b strings.Builder
	var years, holders int
	rest := placeholder
	for {
		before, after, ok := strings.Cut(rest, "<")
		b.WriteString(before)
		if !ok {
			break
		}
		token, tail, ok := strings.Cut(after, ">")
		if !ok {
			return "", fmt.Errorf("copyright placeholder %q has an unterminated token", placeholder)
		}
		if token == "year" {
			years++
			b.WriteString(strconv.Itoa(year))
		} else {
			holders++
			b.WriteString(holder)
		}
		rest = tail
	}
	if years != 1 || holders != 1 {
		return "", fmt.Errorf("copyright placeholder %q has %d <year> and %d holder tokens, want 1 and 1", placeholder, years, holders)
	}
	return b.String(), nil
}

// withFinalNewline may reuse b's backing array; callers pass a slice they own.
func withFinalNewline(b []byte) []byte {
	return append(bytes.TrimRight(b, "\n"), '\n')
}

// manifest is the layout of data/manifest.json that spdxgen writes.
type manifest struct {
	LicenseListVersion string           `json:"licenseListVersion"`
	Tag                string           `json:"tag"`
	Commit             string           `json:"commit"` // the commit Tag names; the data was fetched by it
	Licenses           map[string]entry `json:"licenses"`
}

type entry struct {
	Placeholder string `json:"placeholder"`
	SHA256      string `json:"sha256"`
}

func parseManifest(b []byte) (manifest, error) {
	var m manifest
	if err := json.Unmarshal(b, &m, json.RejectUnknownMembers(true)); err != nil {
		return manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	switch {
	case m.LicenseListVersion == "":
		return manifest{}, fmt.Errorf("manifest: licenseListVersion is empty")
	case m.Tag == "":
		return manifest{}, fmt.Errorf("manifest: tag is empty")
	case len(m.Commit) != 40:
		return manifest{}, fmt.Errorf("manifest: commit %q is not 40 hex digits", m.Commit)
	case len(m.Licenses) == 0:
		return manifest{}, fmt.Errorf("manifest: no licenses")
	}
	for id, e := range m.Licenses {
		if len(e.SHA256) != 64 {
			return manifest{}, fmt.Errorf("manifest: %s: sha256 %q is not 64 hex digits", id, e.SHA256)
		}
	}
	return m, nil
}

// embedded is parsed once at start-up. The data is fixed at build time and the tests parse the same bytes,
// so a failure here is a build defect, not a runtime condition.
var embedded = func() manifest {
	b, err := dataFS.ReadFile("data/manifest.json")
	if err != nil {
		panic("license: embedded data: " + err.Error())
	}
	m, err := parseManifest(b)
	if err != nil {
		panic("license: embedded data: " + err.Error())
	}
	return m
}()
