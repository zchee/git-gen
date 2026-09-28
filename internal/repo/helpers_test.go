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
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// stubSignature is what testdata/signer/ok.sh prints.
const stubSignature = "-----BEGIN PGP SIGNATURE-----\n\nc3R1Yi1zaWduYXR1cmU=\n=AAAA\n-----END PGP SIGNATURE-----\n"

// stubKeyID is the made-up key id on the status line of testdata/signer/fail.sh.
const stubKeyID = "0123456789ABCDEF0123456789ABCDEF01234567"

// fixedNow is the clock of every commit made by the tests; git stores whole seconds.
var fixedNow = time.Date(2026, 9, 28, 3, 30, 15, 0, time.FixedZone("JST", 9*60*60))

// testConfig is a configuration whose author and committer differ, so a test notices when one is used for
// the other.
var testConfig = Config{
	Author:         Identity{Name: "Test Author", Email: "author@example.com"},
	Committer:      Identity{Name: "Test Committer", Email: "committer@example.com"},
	DefaultBranch:  "main",
	SigningFormat:  "openpgp",
	SigningProgram: "gpg",
	SigningKey:     "Test Committer <committer@example.com>",
}

// testdataPath returns the absolute path of a file under the repository's testdata directory.
func testdataPath(t *testing.T, elem ...string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join(append([]string{"..", "..", "testdata"}, elem...)...))
	if err != nil {
		t.Fatalf("testdata path: %v", err)
	}
	return p
}

// stubConfig returns testConfig with the signing program set to a stub under testdata/signer.
func stubConfig(t *testing.T, stub string) Config {
	t.Helper()
	cfg := testConfig
	cfg.SigningProgram = testdataPath(t, "signer", stub)
	return cfg
}

// writeFiles writes each slash-separated path of files under dir, creating parent directories.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

// gitStatus returns the lines of `git status --porcelain=v1` in dir, sorted; nil when the tree is clean.
func gitStatus(t *testing.T, dir string) []string {
	t.Helper()
	var status []string
	for line := range strings.SplitSeq(strings.TrimRight(gitCLI(t, dir, "status", "--porcelain=v1"), "\n"), "\n") {
		if line != "" {
			status = append(status, line)
		}
	}
	slices.Sort(status)
	return status
}

// gitCLI runs the git command line in dir and returns its standard output. It never reads the owner's
// configuration: every GIT_* variable is dropped, HOME and XDG_CONFIG_HOME point into a temporary
// directory, and the global and system files are disabled. Only commands that start no signing or
// verification program may be run through it.
func gitCLI(t *testing.T, dir string, args ...string) string {
	t.Helper()
	home := t.TempDir()
	env := []string{
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
	}
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, "GIT_") || key == "HOME" || key == "XDG_CONFIG_HOME" || key == "LC_ALL" {
			continue
		}
		env = append(env, kv)
	}

	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = env
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v\nstderr:\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return string(out)
}
