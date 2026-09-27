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
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

func TestAttributes(t *testing.T) {
	golden := string(readFile(t, goldenAttributes))
	// The golden file is the header block, a blank line and the go.sum block.
	header, goSum, ok := strings.Cut(golden, "\ngo.sum")
	if !ok {
		t.Fatalf("%s has no go.sum block", goldenAttributes)
	}
	goSum = "go.sum" + goSum
	otherHeader := strings.Replace(header, "# git-gen project", "# someone project", 1)

	c := openCatalog(t, fixtureDir)
	tests := map[string]struct {
		existing string
		author   string
		args     []string
		want     string
	}{
		"success: Go on empty content equals the golden file": {
			author: "git-gen",
			args:   []string{"Go"},
			want:   golden,
		},
		"success: go on empty content equals the golden file": {
			author: "git-gen",
			args:   []string{"go"},
			want:   golden,
		},
		"success: Rust gives the header only": {
			author: "git-gen",
			args:   []string{"Rust"},
			want:   header,
		},
		"success: community/Golang gives the header only": {
			author: "git-gen",
			args:   []string{"community/Golang"},
			want:   header,
		},
		"success: no languages gives the header only": {
			author: "git-gen",
			want:   header,
		},
		"success: own output is unchanged": {
			existing: golden,
			author:   "git-gen",
			args:     []string{"Go"},
			want:     golden,
		},
		"success: go after Rust adds the go.sum block": {
			existing: header,
			author:   "git-gen",
			args:     []string{"go"},
			want:     golden,
		},
		"success: Rust after go changes nothing": {
			existing: golden,
			author:   "git-gen",
			args:     []string{"Rust"},
			want:     golden,
		},
		"success: newline is added before appending": {
			existing: "*.png binary",
			author:   "git-gen",
			args:     []string{"Rust"},
			want:     "*.png binary\n" + header,
		},
		"success: content without a trailing newline and with both blocks is unchanged": {
			existing: strings.TrimSuffix(golden, "\n"),
			author:   "git-gen",
			args:     []string{"go"},
			want:     strings.TrimSuffix(golden, "\n"),
		},
		"success: present go.sum block is not repeated": {
			existing: goSum,
			author:   "git-gen",
			args:     []string{"go"},
			want:     goSum + header,
		},
		"success: header of another author counts as absent": {
			existing: otherHeader,
			author:   "git-gen",
			args:     []string{"Rust"},
			want:     otherHeader + header,
		},
		"success: block text that does not start a line counts as absent": {
			existing: "x" + header,
			author:   "git-gen",
			want:     "x" + header + header,
		},
		"success: user lines are kept in front": {
			existing: "*.png binary\n",
			author:   "git-gen",
			args:     []string{"Go"},
			want:     "*.png binary\n" + golden,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var existing []byte
			if tt.existing != "" {
				existing = []byte(tt.existing)
			}
			got := Attributes(existing, tt.author, resolve(t, c, tt.args...))
			if diff := gocmp.Diff(tt.want, string(got)); diff != "" {
				t.Errorf("Attributes(%q, %q, %q) mismatch (-want +got):\n%s", tt.existing, tt.author, tt.args, diff)
			}
			if again := Attributes(got, tt.author, resolve(t, c, tt.args...)); !bytes.Equal(again, got) {
				t.Errorf("Attributes applied to its own output changed it:\n%s\nto:\n%s", got, again)
			}
		})
	}
}

// TestAttributesSequence checks that running Rust and then
// go, twice, gives one header block and one go.sum block.
func TestAttributesSequence(t *testing.T) {
	golden := readFile(t, goldenAttributes)
	c := openCatalog(t, fixtureDir)
	var got []byte
	for range 2 {
		got = Attributes(got, "git-gen", resolve(t, c, "Rust"))
		got = Attributes(got, "git-gen", resolve(t, c, "go"))
	}
	if diff := gocmp.Diff(string(golden), string(got)); diff != "" {
		t.Errorf("Attributes sequence mismatch (-want +got):\n%s", diff)
	}
	for _, block := range []string{"# git-gen project gitattributes file\n", "go.sum       linguist-vendored\n"} {
		if n := bytes.Count(got, []byte(block)); n != 1 {
			t.Errorf("output holds %q %d times, want once", block, n)
		}
	}
}

// TestAttributesAliasing checks that existing is never written to, even when
// it has spare capacity.
func TestAttributesAliasing(t *testing.T) {
	c := openCatalog(t, fixtureDir)
	langs := resolve(t, c, "go")
	tests := map[string]struct {
		existing string
	}{
		"success: appending to content without a trailing newline": {existing: "*.png binary"},
		"success: appending to content with a trailing newline":    {existing: "*.png binary\n"},
		"success: nothing to append":                               {existing: string(readFile(t, goldenAttributes))},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			buf := make([]byte, len(tt.existing), len(tt.existing)+64)
			copy(buf, tt.existing)
			got := Attributes(buf, "git-gen", langs)
			if spare := buf[len(buf):cap(buf)]; bytes.ContainsFunc(spare, func(r rune) bool { return r != 0 }) {
				t.Errorf("Attributes wrote into the spare capacity of existing: %q", spare)
			}
			if len(got) > 0 {
				got[0] = '!'
			}
			if string(buf) != tt.existing {
				t.Errorf("existing changed to %q, want %q", buf, tt.existing)
			}
		})
	}
}

func TestScaffolds(t *testing.T) {
	c := openCatalog(t, fixtureDir)
	tests := map[string]struct {
		args []string
		want Scaffold
	}{
		"success: none":                  {want: 0},
		"success: go":                    {args: []string{"go"}, want: ScaffoldGo | ScaffoldGoSumAttributes},
		"success: Go":                    {args: []string{"Go"}, want: ScaffoldGo | ScaffoldGoSumAttributes | ScaffoldMakefile},
		"success: go Go":                 {args: []string{"go", "Go"}, want: ScaffoldGo | ScaffoldGoSumAttributes | ScaffoldMakefile},
		"success: community/Golang":      {args: []string{"community/Golang"}, want: ScaffoldGo | ScaffoldMakefile},
		"success: go community/Golang":   {args: []string{"go", "community/Golang"}, want: ScaffoldGo | ScaffoldGoSumAttributes | ScaffoldMakefile},
		"success: languages without any": {args: []string{"go-pkg", "go-simple", "Rust", "Python", "Global/JetBrains", "Alias"}, want: 0},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := Scaffolds(resolve(t, c, tt.args...)); got != tt.want {
				t.Errorf("Scaffolds(%q) = %03b, want %03b", tt.args, got, tt.want)
			}
		})
	}
}
