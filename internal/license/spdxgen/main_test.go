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

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-json-experiment/json"
	"github.com/google/go-cmp/cmp"
)

// The fixtures copy the shape of the v3.29.0 data: the copyright original of BSD-3-Clause ends in ".  ",
// the published BSD line ends in one space, and CC-BY-SA-4.0 has no copyright variable.
const (
	mitText = "MIT License\n\nCopyright (c) <year> <copyright holders>\n\nPermission is hereby granted.\n"
	mitTmpl = `<<beginOptional>>MIT License<<endOptional>>` + "\n\n" +
		`<<var;name="copyright";original="Copyright (c) <year> <copyright holders>  ";match=".{0,5000}">>` + "\n\n" +
		`Permission is hereby granted<<var;name="files";original="";match="\s+(of\s+charge)?">>.` + "\n"
	bsdText = "Copyright (c) <year> <owner>. \n\nRedistribution and use are permitted.\n"
	bsdTmpl = `<<var;name="copyright";original="Copyright (c) <year> <owner>.  ";match=".{0,5000}">>` + "\n\n" +
		`Redistribution and use are permitted.` + "\n"
	ccText = "Attribution-ShareAlike 4.0 International\n"
	ccTmpl = `<<var;name="ccLicensed";original="";match="CC-[ \t\r\n\f]{0,10}licensed">>` +
		`Attribution-ShareAlike 4.0 International` + "\n"
)

type fixture struct {
	status int
	body   string
}

func detailsBody(t *testing.T, id, text, tmpl string) fixture {
	t.Helper()
	b, err := json.Marshal(details{LicenseID: id, LicenseText: text, StandardLicenseTemplate: tmpl})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{status: http.StatusOK, body: string(b)}
}

// baseRoutes serves only the paths under the fixed tag, so a request built with the wrong layout fails.
func baseRoutes(t *testing.T) map[string]fixture {
	t.Helper()
	return map[string]fixture{
		"/v3.29.0/json/licenses.json": {
			status: http.StatusOK,
			body:   `{"licenseListVersion":"3.29.0","licenses":[{"licenseId":"MIT"}],"releaseDate":"2026-09-16T00:00:00Z"}`,
		},
		"/v3.29.0/json/details/MIT.json":          detailsBody(t, "MIT", mitText, mitTmpl),
		"/v3.29.0/json/details/BSD-3-Clause.json": detailsBody(t, "BSD-3-Clause", bsdText, bsdTmpl),
		"/v3.29.0/json/details/CC-BY-SA-4.0.json": detailsBody(t, "CC-BY-SA-4.0", ccText, ccTmpl),
	}
}

func serve(t *testing.T, routes map[string]fixture) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(f.status)
		fmt.Fprint(w, f.body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func readDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	files := make(map[string]string, len(entries))
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[e.Name()] = string(b)
	}
	return files
}

func TestRun(t *testing.T) {
	wantManifest := fmt.Sprintf(`{
  "licenseListVersion": "3.29.0",
  "tag": "v3.29.0",
  "licenses": {
    "BSD-3-Clause": {
      "placeholder": "Copyright (c) <year> <owner>.",
      "sha256": "%s"
    },
    "CC-BY-SA-4.0": {
      "placeholder": "",
      "sha256": "%s"
    },
    "MIT": {
      "placeholder": "Copyright (c) <year> <copyright holders>",
      "sha256": "%s"
    }
  }
}
`, sum(bsdText), sum(ccText), sum(mitText))

	tests := map[string]struct {
		ids    []string
		routes func(map[string]fixture)
		// cancel runs the generator with a canceled context.
		cancel bool
		// outIsFile makes the output path an existing regular file.
		outIsFile bool
		wantErr   string
	}{
		"success: writes texts, manifest and README": {
			ids: []string{"MIT", "BSD-3-Clause", "CC-BY-SA-4.0"},
		},
		"error: placeholder occurs twice in the text": {
			ids: []string{"BSD-3-Clause"},
			routes: func(r map[string]fixture) {
				r["/v3.29.0/json/details/BSD-3-Clause.json"] = detailsBody(t, "BSD-3-Clause", bsdText+bsdText, bsdTmpl)
			},
			wantErr: "occurs 2 times",
		},
		"error: placeholder absent from the text": {
			ids: []string{"MIT"},
			routes: func(r map[string]fixture) {
				r["/v3.29.0/json/details/MIT.json"] = detailsBody(t, "MIT", "MIT License\n", mitTmpl)
			},
			wantErr: "occurs 0 times",
		},
		"error: details not found": {
			ids:     []string{"MIT", "GPL-3.0-only"},
			wantErr: "404 Not Found",
		},
		"error: licenses.json server error": {
			ids: []string{"MIT"},
			routes: func(r map[string]fixture) {
				r["/v3.29.0/json/licenses.json"] = fixture{status: http.StatusInternalServerError, body: "boom"}
			},
			wantErr: "500 Internal Server Error",
		},
		"error: licenses.json is not JSON": {
			ids: []string{"MIT"},
			routes: func(r map[string]fixture) {
				r["/v3.29.0/json/licenses.json"] = fixture{status: http.StatusOK, body: "{"}
			},
			wantErr: "decode",
		},
		"error: empty licenseListVersion": {
			ids: []string{"MIT"},
			routes: func(r map[string]fixture) {
				r["/v3.29.0/json/licenses.json"] = fixture{status: http.StatusOK, body: `{"licenseListVersion":""}`}
			},
			wantErr: "licenseListVersion is empty",
		},
		"error: details carry another licenseId": {
			ids: []string{"MIT"},
			routes: func(r map[string]fixture) {
				r["/v3.29.0/json/details/MIT.json"] = detailsBody(t, "MIT-0", mitText, mitTmpl)
			},
			wantErr: `licenseId "MIT-0"`,
		},
		"error: empty licenseText": {
			ids: []string{"CC-BY-SA-4.0"},
			routes: func(r map[string]fixture) {
				r["/v3.29.0/json/details/CC-BY-SA-4.0.json"] = detailsBody(t, "CC-BY-SA-4.0", "", ccTmpl)
			},
			wantErr: "licenseText is empty",
		},
		"error: malformed template markup": {
			ids: []string{"MIT"},
			routes: func(r map[string]fixture) {
				r["/v3.29.0/json/details/MIT.json"] = detailsBody(t, "MIT", mitText, `<<var;name="copyright";original="Copyright`)
			},
			wantErr: "unterminated original value",
		},
		"error: ID listed twice": {
			ids:     []string{"MIT", "MIT"},
			wantErr: "listed more than once",
		},
		"error: canceled context": {
			ids:     []string{"MIT"},
			cancel:  true,
			wantErr: "context canceled",
		},
		"error: output path is a file": {
			ids:       []string{"MIT"},
			outIsFile: true,
			wantErr:   "not a directory",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			routes := baseRoutes(t)
			if tc.routes != nil {
				tc.routes(routes)
			}
			srv := serve(t, routes)

			out := filepath.Join(t.TempDir(), "data")
			if tc.outIsFile {
				if err := os.WriteFile(out, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			cfg := config{BaseURL: srv.URL, Tag: tag, OutDir: out, IDs: tc.ids, Client: srv.Client()}

			err := run(ctx, &cfg)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("run() error = %v, want one containing %q", err, tc.wantErr)
				}
				t.Logf("error (expected): %v", err)
				if !tc.outIsFile {
					if files := readDir(t, out); len(files) != 0 {
						t.Errorf("run() failed but wrote %v", slices.Sorted(maps.Keys(files)))
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("run() error: %v", err)
			}

			got := readDir(t, out)
			for name, want := range map[string]string{
				"MIT.txt":          mitText,
				"BSD-3-Clause.txt": bsdText,
				"CC-BY-SA-4.0.txt": ccText,
				"manifest.json":    wantManifest,
			} {
				if diff := cmp.Diff(want, got[name]); diff != "" {
					t.Errorf("%s mismatch (-want +got):\n%s", name, diff)
				}
			}
			wantFiles := []string{"BSD-3-Clause.txt", "CC-BY-SA-4.0.txt", "MIT.txt", "README.md", "manifest.json"}
			if diff := cmp.Diff(wantFiles, slices.Sorted(maps.Keys(got))); diff != "" {
				t.Errorf("written files mismatch (-want +got):\n%s", diff)
			}
			for _, want := range []string{
				"SPDX License List", "3.29.0", "v3.29.0", "CC-BY-3.0",
				"Linux Foundation and its Contributors",
				srv.URL + "/v3.29.0/json/",
			} {
				if !strings.Contains(got["README.md"], want) {
					t.Errorf("README.md does not contain %q:\n%s", want, got["README.md"])
				}
			}

			if err := run(ctx, &cfg); err != nil {
				t.Fatalf("second run() error: %v", err)
			}
			if diff := cmp.Diff(got, readDir(t, out)); diff != "" {
				t.Errorf("second run changed the output (-first +second):\n%s", diff)
			}
		})
	}
}

func TestCopyrightPlaceholder(t *testing.T) {
	tests := map[string]struct {
		tmpl    string
		want    string
		wantErr bool
	}{
		"success: original containing '>' is read by its quotes": {
			tmpl: bsdTmpl,
			want: "Copyright (c) <year> <owner>.",
		},
		"success: variables with other names are skipped": {
			tmpl: `<<var;name="bullet";original="1.";match=".{0,20}">> text ` +
				`<<var;name="copyright";original="[yyyy] [name of copyright owner]";match=".+">>`,
			want: "[yyyy] [name of copyright owner]",
		},
		"success: backslashes in a match value do not end it": {
			tmpl: `<<var;name="x";original="";match="a\s+\"b\"\\">> ` +
				`<<var;name="copyright";original=" (c) <year> <who> ";match=".+">>`,
			want: "(c) <year> <who>",
		},
		"success: no copyright variable": {
			tmpl: ccTmpl,
			want: "",
		},
		"success: no markup": {
			tmpl: "plain text\n",
			want: "",
		},
		"error: two copyright variables": {
			tmpl:    bsdTmpl + bsdTmpl,
			wantErr: true,
		},
		"error: unterminated value": {
			tmpl:    `<<var;name="copyright";original="abc`,
			wantErr: true,
		},
		"error: fields not separated by ';'": {
			tmpl:    `<<var;name="copyright" original="abc">>`,
			wantErr: true,
		},
		"error: field name with punctuation": {
			tmpl:    `<<var;na-me="copyright">>`,
			wantErr: true,
		},
		"error: no fields": {
			tmpl:    `<<var;>>`,
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := copyrightPlaceholder(tc.tmpl)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("copyrightPlaceholder() = %q, want error", got)
				}
				t.Logf("error (expected): %v", err)
				return
			}
			if err != nil {
				t.Fatalf("copyrightPlaceholder() error: %v", err)
			}
			if got != tc.want {
				t.Errorf("copyrightPlaceholder() = %q, want %q", got, tc.want)
			}
		})
	}
}
