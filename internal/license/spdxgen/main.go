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

// Command spdxgen writes the SPDX license data that package license embeds.
//
// Usage:
//
//	spdxgen <SPDX ID>...
//
// It fetches json/licenses.json and json/details/<ID>.json of the SPDX License List at a fixed tag and
// writes <ID>.txt, manifest.json and README.md into the data directory. Nothing is written unless every
// license passes validation, so a failed run leaves the previous data intact.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
)

const (
	// tag is the release of spdx/license-list-data the data is generated from.
	tag = "v3.29.0"

	defaultBaseURL = "https://raw.githubusercontent.com/spdx/license-list-data"
	fetchTimeout   = 30 * time.Second
	outDir         = "data"
)

// manifest is the layout of manifest.json that package license reads.
type manifest struct {
	LicenseListVersion string           `json:"licenseListVersion"`
	Tag                string           `json:"tag"`
	Licenses           map[string]entry `json:"licenses"`
}

// entry carries no omitzero: an empty placeholder means the text has no copyright line and must be written.
type entry struct {
	Placeholder string `json:"placeholder"`
	SHA256      string `json:"sha256"`
}

type details struct {
	LicenseID               string `json:"licenseId"`
	LicenseText             string `json:"licenseText"`
	StandardLicenseTemplate string `json:"standardLicenseTemplate"`
}

type config struct {
	BaseURL string
	Tag     string
	OutDir  string
	IDs     []string
	Client  *http.Client
}

func (c *config) url(name string) string {
	return c.BaseURL + "/" + c.Tag + "/json/" + name
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("spdxgen: ")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: spdxgen <SPDX ID>...\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, &config{
		BaseURL: defaultBaseURL,
		Tag:     tag,
		OutDir:  outDir,
		IDs:     flag.Args(),
		Client:  &http.Client{Timeout: fetchTimeout},
	})
	stop()
	if err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, cfg *config) error {
	var list struct {
		LicenseListVersion string `json:"licenseListVersion"`
	}
	listURL := cfg.url("licenses.json")
	if err := fetchJSON(ctx, cfg.Client, listURL, &list); err != nil {
		return err
	}
	if list.LicenseListVersion == "" {
		return fmt.Errorf("%s: licenseListVersion is empty", listURL)
	}

	m := manifest{
		LicenseListVersion: list.LicenseListVersion,
		Tag:                cfg.Tag,
		Licenses:           make(map[string]entry, len(cfg.IDs)),
	}
	texts := make(map[string]string, len(cfg.IDs))
	for _, id := range cfg.IDs {
		if _, dup := m.Licenses[id]; dup {
			return fmt.Errorf("%s: listed more than once", id)
		}
		var d details
		if err := fetchJSON(ctx, cfg.Client, cfg.url("details/"+id+".json"), &d); err != nil {
			return err
		}
		e, err := check(id, d)
		if err != nil {
			return err
		}
		m.Licenses[id] = e
		texts[id] = d.LicenseText
	}

	return write(cfg, m, texts)
}

// check validates one license and derives its manifest entry.
func check(id string, d details) (entry, error) {
	if d.LicenseID != id {
		return entry{}, fmt.Errorf("%s: details carry licenseId %q", id, d.LicenseID)
	}
	if d.LicenseText == "" {
		return entry{}, fmt.Errorf("%s: licenseText is empty", id)
	}
	placeholder, err := copyrightPlaceholder(d.StandardLicenseTemplate)
	if err != nil {
		return entry{}, fmt.Errorf("%s: %w", id, err)
	}
	if placeholder != "" {
		if n := strings.Count(d.LicenseText, placeholder); n != 1 {
			return entry{}, fmt.Errorf("%s: copyright placeholder %q occurs %d times in licenseText, want 1", id, placeholder, n)
		}
	}
	sum := sha256.Sum256([]byte(d.LicenseText))
	return entry{Placeholder: placeholder, SHA256: hex.EncodeToString(sum[:])}, nil
}

func fetchJSON(ctx context.Context, client *http.Client, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", url, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch %s: %s", url, resp.Status)
	}
	if err := json.UnmarshalRead(resp.Body, v); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	return nil
}

// copyrightPlaceholder returns the trimmed original of the variable named "copyright" in an SPDX license
// template, or "" when the template has none. Fields are read as quoted strings because an original may
// contain '>' (for example "Copyright (c) <year> <owner>.  ").
func copyrightPlaceholder(tmpl string) (string, error) {
	const open = "<<var;"
	var originals []string
	rest := tmpl
	for {
		_, after, ok := strings.Cut(rest, open)
		if !ok {
			break
		}
		fields, tail, err := parseVar(after)
		if err != nil {
			return "", err
		}
		if fields["name"] == "copyright" {
			originals = append(originals, fields["original"])
		}
		rest = tail
	}
	switch len(originals) {
	case 0:
		return "", nil
	case 1:
		return strings.TrimSpace(originals[0]), nil
	default:
		return "", fmt.Errorf("template has %d copyright variables, want at most 1", len(originals))
	}
}

// parseVar reads the `key="value";...>>` fields that follow "<<var;" and returns them with the text after
// the closing ">>". A backslash inside a value keeps the next byte from ending it; values are returned raw.
func parseVar(s string) (fields map[string]string, rest string, err error) {
	fields = make(map[string]string)
	rest = s
	for {
		key, after, ok := strings.Cut(rest, `="`)
		if !ok || !isFieldName(key) {
			return nil, "", fmt.Errorf("malformed <<var>> markup near %q", snippet(rest))
		}
		value, tail, ok := quoted(after)
		if !ok {
			return nil, "", fmt.Errorf("unterminated %s value in <<var>> markup near %q", key, snippet(rest))
		}
		fields[key] = value
		if next, ok := strings.CutPrefix(tail, ";"); ok {
			rest = next
			continue
		}
		if next, ok := strings.CutPrefix(tail, ">>"); ok {
			return fields, next, nil
		}
		return nil, "", fmt.Errorf("malformed <<var>> markup near %q", snippet(tail))
	}
}

func isFieldName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

func quoted(s string) (value, rest string, ok bool) {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}

func snippet(s string) string {
	return s[:min(len(s), 40)]
}

var readme = template.Must(template.New("README.md").Parse(`# SPDX license data

Generated by ` + "`go generate ./internal/license/...`" + `. Do not edit these files by hand.

- Data: SPDX License List, version {{.Version}}
- Source: ` + "`{{.Source}}`" + ` (tag ` + "`{{.Tag}}`" + `)
- Copyright: Linux Foundation and its Contributors
- License: [CC-BY-3.0](https://creativecommons.org/licenses/by/3.0/)

` + "`<ID>.txt`" + ` is the ` + "`licenseText`" + ` of ` + "`json/details/<ID>.json`" + `, unchanged.
` + "`manifest.json`" + ` records the list version, the tag, and for each ID the copyright placeholder and the
SHA-256 of the text.

Changes: when git-gen writes a ` + "`LICENSE`" + ` file, it replaces the line that holds the copyright placeholder
with a copyright line that names the year and the copyright holder. The files in this directory are not
modified.
`))

func write(cfg *config, m manifest, texts map[string]string) error {
	manifestJSON, err := json.Marshal(m, json.Deterministic(true), jsontext.WithIndent("  "))
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	manifestJSON = append(manifestJSON, '\n')

	var readmeText strings.Builder
	if err := readme.Execute(&readmeText, struct{ Version, Tag, Source string }{
		Version: m.LicenseListVersion,
		Tag:     m.Tag,
		Source:  cfg.BaseURL + "/" + cfg.Tag + "/json/",
	}); err != nil {
		return fmt.Errorf("render README.md: %w", err)
	}

	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", cfg.OutDir, err)
	}
	files := map[string][]byte{
		"manifest.json": manifestJSON,
		"README.md":     []byte(readmeText.String()),
	}
	for id, text := range texts {
		files[id+".txt"] = []byte(text)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(cfg.OutDir, name), content, 0o644); err != nil { //nolint:gosec // G306: public data committed to the repository; 0600 would hide it from other users and build tools.
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return nil
}
