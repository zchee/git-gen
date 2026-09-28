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

package license

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
	"github.com/google/licensecheck"
)

// testVars is the input of every rendering test; the year is fixed so no test depends on the clock.
var testVars = Vars{Author: "foo", Owner: "Jane Doe", Year: 2026}

// readData reads a generated file from disk, independently of the embedded copy.
func readData(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("data", name))
	if err != nil {
		t.Fatalf("read data/%s: %v", name, err)
	}
	return b
}

func readManifest(t *testing.T) manifest {
	t.Helper()
	m, err := parseManifest(readData(t, "manifest.json"))
	if err != nil {
		t.Fatalf("parse data/manifest.json: %v", err)
	}
	return m
}

// assertCoversNames fails unless the case names exactly cover Names() (except the listed exclusions), so a
// new table row cannot go untested.
func assertCoversNames(t *testing.T, covered []string, exclude ...string) {
	t.Helper()
	var want []string
	for _, name := range Names() {
		if !slices.Contains(exclude, name) {
			want = append(want, name)
		}
	}
	slices.Sort(want)
	got := slices.Sorted(slices.Values(covered))
	if diff := gocmp.Diff(want, got); diff != "" {
		t.Errorf("test cases do not cover the accepted names (-want +got):\n%s", diff)
	}
}

func TestLookup(t *testing.T) {
	tests := map[string]struct {
		name   string
		want   License
		wantOK bool
	}{
		"success: bsd": {
			name:   "bsd",
			want:   License{Name: "bsd", SPDXID: "BSD-3-Clause", Holder: HolderProject},
			wantOK: true,
		},
		"success: bsd-project resolves to bsd": {
			name:   "bsd-project",
			want:   License{Name: "bsd", SPDXID: "BSD-3-Clause", Holder: HolderProject},
			wantOK: true,
		},
		"success: bsd-owner": {
			name:   "bsd-owner",
			want:   License{Name: "bsd-owner", SPDXID: "BSD-3-Clause", Holder: HolderOwner},
			wantOK: true,
		},
		"success: bsd-go": {
			name:   "bsd-go",
			want:   License{Name: "bsd-go", SPDXID: "BSD-3-Clause", Holder: HolderGoAuthors},
			wantOK: true,
		},
		"success: mit": {
			name:   "mit",
			want:   License{Name: "mit", SPDXID: "MIT", Holder: HolderProject},
			wantOK: true,
		},
		"success: mit-project resolves to mit": {
			name:   "mit-project",
			want:   License{Name: "mit", SPDXID: "MIT", Holder: HolderProject},
			wantOK: true,
		},
		"success: mit-owner": {
			name:   "mit-owner",
			want:   License{Name: "mit-owner", SPDXID: "MIT", Holder: HolderOwner},
			wantOK: true,
		},
		"success: apache2": {
			name:   "apache2",
			want:   License{Name: "apache2", SPDXID: "Apache-2.0", Holder: HolderNone},
			wantOK: true,
		},
		"success: Apache2 resolves to apache2": {
			name:   "Apache2",
			want:   License{Name: "apache2", SPDXID: "Apache-2.0", Holder: HolderNone},
			wantOK: true,
		},
		"success: CC4": {
			name:   "CC4",
			want:   License{Name: "CC4", SPDXID: "CC-BY-SA-4.0", Holder: HolderNone},
			wantOK: true,
		},
		"success: CC-BY-SA-4.0 resolves to CC4": {
			name:   "CC-BY-SA-4.0",
			want:   License{Name: "CC4", SPDXID: "CC-BY-SA-4.0", Holder: HolderNone},
			wantOK: true,
		},
		"success: none has no SPDX ID": {
			name:   "none",
			want:   License{Name: "none"},
			wantOK: true,
		},
		"error: unknown name": {name: "gpl"},
		"error: empty name":   {name: ""},
		"error: names are case-sensitive": {
			name: "MIT",
		},
		"error: an SPDX ID is not an alias unless listed": {
			name: "BSD-3-Clause",
		},
	}

	var covered []string
	for name, tc := range tests {
		if tc.wantOK {
			covered = append(covered, tc.name)
		}
		t.Run(name, func(t *testing.T) {
			got, ok := Lookup(tc.name)
			if ok != tc.wantOK {
				t.Fatalf("Lookup(%q) ok = %t, want %t", tc.name, ok, tc.wantOK)
			}
			if diff := gocmp.Diff(tc.want, got); diff != "" {
				t.Errorf("Lookup(%q) mismatch (-want +got):\n%s", tc.name, diff)
			}
		})
	}
	assertCoversNames(t, covered)
}

func TestNames(t *testing.T) {
	tests := map[string]struct {
		want []string
	}{
		"success: every alias in table order": {
			want: []string{
				"bsd", "bsd-project", "bsd-owner", "bsd-go",
				"mit", "mit-project", "mit-owner",
				"apache2", "Apache2",
				"CC4", "CC-BY-SA-4.0",
				"none",
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := Names()
			if diff := gocmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("Names() mismatch (-want +got):\n%s", diff)
			}
			got[0] = "mutated"
			if again := Names(); again[0] != tc.want[0] {
				t.Errorf("Names() shares its backing array with the caller: got %q after mutation", again[0])
			}
		})
	}
}

// TestRender checks that every accepted name renders the embedded text with only the copyright line changed.
func TestRender(t *testing.T) {
	tests := map[string]struct {
		name string
		// wantLine is the filled copyright line; empty means the output must equal the embedded text.
		wantLine string
		wantNil  bool
	}{
		"success: bsd names the project authors": {
			name:     "bsd",
			wantLine: "Copyright (c) 2026 The foo Authors.",
		},
		"success: bsd-project names the project authors": {
			name:     "bsd-project",
			wantLine: "Copyright (c) 2026 The foo Authors.",
		},
		"success: bsd-owner names the owner": {
			name:     "bsd-owner",
			wantLine: "Copyright (c) 2026 Jane Doe.",
		},
		"success: bsd-go names The Go Authors": {
			name:     "bsd-go",
			wantLine: "Copyright (c) 2026 The Go Authors.",
		},
		"success: mit names the project authors": {
			name:     "mit",
			wantLine: "Copyright (c) 2026 The foo Authors",
		},
		"success: mit-project names the project authors": {
			name:     "mit-project",
			wantLine: "Copyright (c) 2026 The foo Authors",
		},
		"success: mit-owner names the owner": {
			name:     "mit-owner",
			wantLine: "Copyright (c) 2026 Jane Doe",
		},
		"success: apache2 is the embedded text": {name: "apache2"},
		"success: Apache2 is the embedded text": {name: "Apache2"},
		"success: CC4 is the embedded text":     {name: "CC4"},
		"success: CC-BY-SA-4.0 is the embedded text": {
			name: "CC-BY-SA-4.0",
		},
		"success: none renders nothing": {
			name:    "none",
			wantNil: true,
		},
	}

	covered := make([]string, 0, len(tests))
	for name, tc := range tests {
		covered = append(covered, tc.name)
		t.Run(name, func(t *testing.T) {
			l, ok := Lookup(tc.name)
			if !ok {
				t.Fatalf("Lookup(%q) failed", tc.name)
			}
			got, err := l.Render(testVars)
			if err != nil {
				t.Fatalf("Render(%+v) error: %v", testVars, err)
			}
			if tc.wantNil {
				if got != nil {
					t.Fatalf("Render() = %q, want nil", got)
				}
				return
			}

			if !bytes.HasSuffix(got, []byte("\n")) || bytes.HasSuffix(got, []byte("\n\n")) {
				t.Errorf("Render() does not end with exactly one newline: tail %q", got[max(len(got)-8, 0):])
			}
			text := readData(t, l.SPDXID+".txt")
			if tc.wantLine == "" {
				if diff := gocmp.Diff(string(text), string(got)); diff != "" {
					t.Errorf("Render() differs from data/%s.txt (-want +got):\n%s", l.SPDXID, diff)
				}
				return
			}

			textLines := strings.Split(string(text), "\n")
			gotLines := strings.Split(string(got), "\n")
			if len(gotLines) != len(textLines) {
				t.Fatalf("Render() has %d lines, data/%s.txt has %d", len(gotLines), l.SPDXID, len(textLines))
			}
			var changed []int
			for i := range textLines {
				if gotLines[i] != textLines[i] {
					changed = append(changed, i)
				}
			}
			if len(changed) != 1 {
				t.Fatalf("Render() changed lines %v, want exactly the copyright line", changed)
			}
			i := changed[0]
			if !strings.Contains(textLines[i], "<year>") {
				t.Errorf("Render() changed line %d %q, which is not the copyright line", i+1, textLines[i])
			}
			if gotLines[i] != tc.wantLine {
				t.Errorf("copyright line = %q, want %q", gotLines[i], tc.wantLine)
			}
		})
	}
	assertCoversNames(t, covered)
}

func TestRenderVars(t *testing.T) {
	tests := map[string]struct {
		license License
		vars    Vars
		wantErr bool
	}{
		"success: an unfilled license needs no vars": {
			license: License{Name: "apache2", SPDXID: "Apache-2.0", Holder: HolderNone},
			vars:    Vars{},
		},
		"success: The Go Authors needs no author or owner": {
			license: License{Name: "bsd-go", SPDXID: "BSD-3-Clause", Holder: HolderGoAuthors},
			vars:    Vars{Year: 2026},
		},
		"error: project holder without author": {
			license: License{Name: "bsd", SPDXID: "BSD-3-Clause", Holder: HolderProject},
			vars:    Vars{Owner: "Jane Doe", Year: 2026},
			wantErr: true,
		},
		"error: owner holder without owner": {
			license: License{Name: "mit-owner", SPDXID: "MIT", Holder: HolderOwner},
			vars:    Vars{Author: "foo", Year: 2026},
			wantErr: true,
		},
		"error: year is zero": {
			license: License{Name: "mit", SPDXID: "MIT", Holder: HolderProject},
			vars:    Vars{Author: "foo"},
			wantErr: true,
		},
		"error: year is negative": {
			license: License{Name: "bsd-go", SPDXID: "BSD-3-Clause", Holder: HolderGoAuthors},
			vars:    Vars{Year: -1},
			wantErr: true,
		},
		"error: SPDX ID without embedded text": {
			license: License{Name: "gpl", SPDXID: "GPL-3.0-only", Holder: HolderNone},
			vars:    testVars,
			wantErr: true,
		},
		"error: unknown holder": {
			license: License{Name: "mit", SPDXID: "MIT", Holder: Holder(42)},
			vars:    testVars,
			wantErr: true,
		},
		"error: placeholder without <year> token cannot be filled": {
			license: License{Name: "apache2", SPDXID: "Apache-2.0", Holder: HolderProject},
			vars:    testVars,
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := tc.license.Render(tc.vars)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Render(%+v) = %d bytes, want error", tc.vars, len(got))
				}
				t.Logf("error (expected): %v", err)
				return
			}
			if err != nil {
				t.Fatalf("Render(%+v) error: %v", tc.vars, err)
			}
			if len(got) == 0 {
				t.Errorf("Render(%+v) returned no content", tc.vars)
			}
		})
	}
}

func TestFillCopyright(t *testing.T) {
	tests := map[string]struct {
		text        string
		placeholder string
		holder      string
		want        string
		wantErr     bool
	}{
		"success: trailing space of the line does not survive": {
			text:        "A\nCopyright (c) <year> <owner>. \nB\n",
			placeholder: "Copyright (c) <year> <owner>.",
			holder:      "Jane",
			want:        "A\nCopyright (c) 2026 Jane.\nB\n",
		},
		"success: the whole line is replaced": {
			text:        "A\n  Copyright <year> <who>  and more\nB\n",
			placeholder: "Copyright <year> <who>",
			holder:      "Jane",
			want:        "A\nCopyright 2026 Jane\nB\n",
		},
		"success: placeholder on the first line": {
			text:        "Copyright <year> <who>\nB\n",
			placeholder: "Copyright <year> <who>",
			holder:      "Jane",
			want:        "Copyright 2026 Jane\nB\n",
		},
		"success: placeholder on a last line without newline": {
			text:        "A\nCopyright <year> <who>",
			placeholder: "Copyright <year> <who>",
			holder:      "Jane",
			want:        "A\nCopyright 2026 Jane\n",
		},
		"success: trailing newlines collapse to one": {
			text:        "Copyright <year> <who>\nB\n\n\n",
			placeholder: "Copyright <year> <who>",
			holder:      "Jane",
			want:        "Copyright 2026 Jane\nB\n",
		},
		"success: holder token before the year token": {
			text:        "(c) <who>, <year>\n",
			placeholder: "(c) <who>, <year>",
			holder:      "Jane",
			want:        "(c) Jane, 2026\n",
		},
		"success: holder text is not scanned for tokens": {
			text:        "Copyright <year> <who>\n",
			placeholder: "Copyright <year> <who>",
			holder:      "Jane <year>",
			want:        "Copyright 2026 Jane <year>\n",
		},
		"error: placeholder absent from text": {
			text:        "A\nB\n",
			placeholder: "Copyright <year> <who>",
			holder:      "Jane",
			wantErr:     true,
		},
		"error: placeholder twice in text": {
			text:        "Copyright <year> <who>\nCopyright <year> <who>\n",
			placeholder: "Copyright <year> <who>",
			holder:      "Jane",
			wantErr:     true,
		},
		"error: empty placeholder": {
			text:        "A\n",
			placeholder: "",
			holder:      "Jane",
			wantErr:     true,
		},
		"error: placeholder without tokens": {
			text:        "Copyright [yyyy] [name of copyright owner]\n",
			placeholder: "Copyright [yyyy] [name of copyright owner]",
			holder:      "Jane",
			wantErr:     true,
		},
		"error: two holder tokens": {
			text:        "Copyright <year> <a> <b>\n",
			placeholder: "Copyright <year> <a> <b>",
			holder:      "Jane",
			wantErr:     true,
		},
		"error: two year tokens": {
			text:        "Copyright <year>-<year> <who>\n",
			placeholder: "Copyright <year>-<year> <who>",
			holder:      "Jane",
			wantErr:     true,
		},
		"error: unterminated token": {
			text:        "Copyright <year> <who\n",
			placeholder: "Copyright <year> <who",
			holder:      "Jane",
			wantErr:     true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := fillCopyright([]byte(tc.text), tc.placeholder, tc.holder, 2026)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("fillCopyright() = %q, want error", got)
				}
				t.Logf("error (expected): %v", err)
				return
			}
			if err != nil {
				t.Fatalf("fillCopyright() error: %v", err)
			}
			if diff := gocmp.Diff(tc.want, string(got)); diff != "" {
				t.Errorf("fillCopyright() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestManifestIDs pins the go:generate argument list and the data directory to the table.
func TestManifestIDs(t *testing.T) {
	var tableIDs []string
	for _, row := range table {
		if row.spdxID != "" && !slices.Contains(tableIDs, row.spdxID) {
			tableIDs = append(tableIDs, row.spdxID)
		}
	}
	slices.Sort(tableIDs)

	txtFiles, err := filepath.Glob(filepath.Join("data", "*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	fileIDs := make([]string, 0, len(txtFiles))
	for _, f := range txtFiles {
		fileIDs = append(fileIDs, strings.TrimSuffix(filepath.Base(f), ".txt"))
	}
	slices.Sort(fileIDs)

	tests := map[string]struct {
		got []string
	}{
		"success: manifest IDs equal the table IDs": {
			got: slices.Sorted(maps.Keys(readManifest(t).Licenses)),
		},
		"success: data/*.txt files equal the table IDs": {
			got: fileIDs,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if diff := gocmp.Diff(tableIDs, tc.got); diff != "" {
				t.Errorf("IDs mismatch (-table +got):\n%s", diff)
			}
		})
	}
}

// TestManifestData checks each embedded text against its manifest SHA-256, plus the invariants Render relies on.
func TestManifestData(t *testing.T) {
	m := readManifest(t)
	if diff := gocmp.Diff(m, embedded); diff != "" {
		t.Fatalf("embedded manifest differs from data/manifest.json (-disk +embedded):\n%s", diff)
	}

	tests := map[string]struct {
		id string
		// wantPlaceholders is how often the manifest placeholder occurs in the text.
		wantPlaceholders int
	}{
		"success: MIT":          {id: "MIT", wantPlaceholders: 1},
		"success: BSD-3-Clause": {id: "BSD-3-Clause", wantPlaceholders: 1},
		"success: Apache-2.0":   {id: "Apache-2.0", wantPlaceholders: 1},
		"success: CC-BY-SA-4.0 has no copyright variable": {
			id:               "CC-BY-SA-4.0",
			wantPlaceholders: 0,
		},
	}
	if len(tests) != len(m.Licenses) {
		t.Errorf("tests cover %d IDs, manifest has %d", len(tests), len(m.Licenses))
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			e, ok := m.Licenses[tc.id]
			if !ok {
				t.Fatalf("manifest has no entry for %s", tc.id)
			}
			text := readData(t, tc.id+".txt")
			sum := sha256.Sum256(text)
			if got := hex.EncodeToString(sum[:]); got != e.SHA256 {
				t.Errorf("sha256(data/%s.txt) = %s, manifest says %s", tc.id, got, e.SHA256)
			}
			embeddedText, err := dataFS.ReadFile("data/" + tc.id + ".txt")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(embeddedText, text) {
				t.Errorf("embedded data/%s.txt differs from the file on disk", tc.id)
			}
			got := 0
			if e.Placeholder != "" {
				got = bytes.Count(text, []byte(e.Placeholder))
			}
			if got != tc.wantPlaceholders {
				t.Errorf("placeholder %q occurs %d times in data/%s.txt, want %d", e.Placeholder, got, tc.id, tc.wantPlaceholders)
			}
		})
	}
}

// TestLicensecheck checks that a license detector recognizes every rendered LICENSE.
func TestLicensecheck(t *testing.T) {
	tests := map[string]struct {
		name   string
		wantID string
	}{
		"success: bsd":          {name: "bsd", wantID: "BSD-3-Clause"},
		"success: bsd-project":  {name: "bsd-project", wantID: "BSD-3-Clause"},
		"success: bsd-owner":    {name: "bsd-owner", wantID: "BSD-3-Clause"},
		"success: bsd-go":       {name: "bsd-go", wantID: "BSD-3-Clause"},
		"success: mit":          {name: "mit", wantID: "MIT"},
		"success: mit-project":  {name: "mit-project", wantID: "MIT"},
		"success: mit-owner":    {name: "mit-owner", wantID: "MIT"},
		"success: apache2":      {name: "apache2", wantID: "Apache-2.0"},
		"success: Apache2":      {name: "Apache2", wantID: "Apache-2.0"},
		"success: CC4":          {name: "CC4", wantID: "CC-BY-SA-4.0"},
		"success: CC-BY-SA-4.0": {name: "CC-BY-SA-4.0", wantID: "CC-BY-SA-4.0"},
	}

	covered := make([]string, 0, len(tests))
	for name, tc := range tests {
		covered = append(covered, tc.name)
		t.Run(name, func(t *testing.T) {
			l, ok := Lookup(tc.name)
			if !ok {
				t.Fatalf("Lookup(%q) failed", tc.name)
			}
			out, err := l.Render(testVars)
			if err != nil {
				t.Fatalf("Render() error: %v", err)
			}
			cov := licensecheck.Scan(out)
			t.Logf("licensecheck: %.1f%% %+v", cov.Percent, cov.Match)
			found := slices.ContainsFunc(cov.Match, func(m licensecheck.Match) bool { return m.ID == tc.wantID })
			if !found {
				t.Errorf("licensecheck matches %+v, want ID %s", cov.Match, tc.wantID)
			}
			if cov.Percent < 90 {
				t.Errorf("licensecheck coverage = %.1f%%, want at least 90%%", cov.Percent)
			}
		})
	}
	assertCoversNames(t, covered, "none")
}

// TestAttribution checks the attribution in data/README.md against the manifest.
func TestAttribution(t *testing.T) {
	readme := string(readData(t, "README.md"))
	m := readManifest(t)

	tests := map[string]struct {
		want string
	}{
		"success: names the data":             {want: "SPDX License List"},
		"success: names the license":          {want: "CC-BY-3.0"},
		"success: links the license":          {want: "https://creativecommons.org/licenses/by/3.0/"},
		"success: names the holder":           {want: "Linux Foundation and its Contributors"},
		"success: states the change":          {want: "replaces the line that holds the copyright placeholder"},
		"success: names the manifest tag":     {want: m.Tag},
		"success: names the manifest version": {want: "version " + m.LicenseListVersion},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(readme, tc.want) {
				t.Errorf("data/README.md does not contain %q", tc.want)
			}
		})
	}

	if m.LicenseListVersion != "3.29.0" {
		t.Errorf("manifest version = %q, want %q", m.LicenseListVersion, "3.29.0")
	}
}

// TestDataSource checks that the data was fetched by the commit that the tag v3.29.0 names, not by the tag,
// which upstream can move: the manifest keeps the tag and records the commit, and the README names the
// source by the commit. The commit is refs/tags/v3.29.0^{} of https://github.com/spdx/license-list-data.
func TestDataSource(t *testing.T) {
	const commit = "31ba1a50e5397e00a304dbadc76531740e89ee48"
	m := readManifest(t)
	if m.Commit != commit {
		t.Errorf("manifest commit = %q, want %q", m.Commit, commit)
	}
	if m.Tag != "v3.29.0" {
		t.Errorf("manifest tag = %q, want %q", m.Tag, "v3.29.0")
	}
	source := "https://raw.githubusercontent.com/spdx/license-list-data/" + commit + "/json/"
	if readme := string(readData(t, "README.md")); !strings.Contains(readme, source) {
		t.Errorf("data/README.md does not contain the source URL %q", source)
	}
}

func TestParseManifest(t *testing.T) {
	const sum = "0000000000000000000000000000000000000000000000000000000000000000"
	tests := map[string]struct {
		input   string
		want    manifest
		wantErr bool
	}{
		"success: empty placeholder is kept": {
			input: `{"licenseListVersion":"3.29.0","tag":"v3.29.0","commit":"31ba1a50e5397e00a304dbadc76531740e89ee48","licenses":{"CC-BY-SA-4.0":{"placeholder":"","sha256":"` + sum + `"}}}`,
			want: manifest{
				LicenseListVersion: "3.29.0",
				Tag:                "v3.29.0",
				Commit:             "31ba1a50e5397e00a304dbadc76531740e89ee48",
				Licenses:           map[string]entry{"CC-BY-SA-4.0": {SHA256: sum}},
			},
		},
		"error: not JSON": {
			input:   `{`,
			wantErr: true,
		},
		"error: unknown member": {
			input:   `{"licenseListVersion":"3.29.0","tag":"v3.29.0","commit":"31ba1a50e5397e00a304dbadc76531740e89ee48","extra":1,"licenses":{"MIT":{"placeholder":"","sha256":"` + sum + `"}}}`,
			wantErr: true,
		},
		"error: empty version": {
			input:   `{"licenseListVersion":"","tag":"v3.29.0","commit":"31ba1a50e5397e00a304dbadc76531740e89ee48","licenses":{"MIT":{"placeholder":"","sha256":"` + sum + `"}}}`,
			wantErr: true,
		},
		"error: empty tag": {
			input:   `{"licenseListVersion":"3.29.0","tag":"","commit":"31ba1a50e5397e00a304dbadc76531740e89ee48","licenses":{"MIT":{"placeholder":"","sha256":"` + sum + `"}}}`,
			wantErr: true,
		},
		"error: no commit": {
			input:   `{"licenseListVersion":"3.29.0","tag":"v3.29.0","licenses":{"MIT":{"placeholder":"","sha256":"` + sum + `"}}}`,
			wantErr: true,
		},
		"error: short commit": {
			input:   `{"licenseListVersion":"3.29.0","tag":"v3.29.0","commit":"31ba1a5","licenses":{"MIT":{"placeholder":"","sha256":"` + sum + `"}}}`,
			wantErr: true,
		},
		"error: no licenses": {
			input:   `{"licenseListVersion":"3.29.0","tag":"v3.29.0","commit":"31ba1a50e5397e00a304dbadc76531740e89ee48","licenses":{}}`,
			wantErr: true,
		},
		"error: short sha256": {
			input:   `{"licenseListVersion":"3.29.0","tag":"v3.29.0","commit":"31ba1a50e5397e00a304dbadc76531740e89ee48","licenses":{"MIT":{"placeholder":"","sha256":"abc"}}}`,
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := parseManifest([]byte(tc.input))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseManifest() = %+v, want error", got)
				}
				t.Logf("error (expected): %v", err)
				return
			}
			if err != nil {
				t.Fatalf("parseManifest() error: %v", err)
			}
			if diff := gocmp.Diff(tc.want, got); diff != "" {
				t.Errorf("parseManifest() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
