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
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// realSigningEnv switches on TestRealSigning.
const realSigningEnv = "GIT_GEN_TEST_REAL_SIGNING"

// TestRealSigning is the end-to-end signing test. It is skipped unless GIT_GEN_TEST_REAL_SIGNING=1, because
// it reads the owner's git configuration, signs three commits with the real signing program and key (which
// may ask for a passphrase), and verifies them with the git command line, which starts the verification
// program. It logs no identity, key id or signature.
func TestRealSigning(t *testing.T) {
	if os.Getenv(realSigningEnv) != "1" {
		t.Skipf("set %s=1 to sign with the real git configuration", realSigningEnv)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home directory: %v", err)
	}
	cfg, _, err := LoadConfig(ConfigOptions{Getenv: os.Getenv, Home: home})
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	signer, err := NewSigner(t.Context(), cfg)
	if err != nil {
		t.Fatalf("NewSigner() error = %v", err)
	}

	dir := t.TempDir()
	writeFiles(t, dir, generatedFiles(goGitignore))
	r, existed, err := Open(Options{Dir: dir, Config: cfg, Signer: signer, Now: time.Now})
	if err != nil || existed {
		t.Fatalf("Open() = %t, %v, want a new repository", existed, err)
	}
	hashes := make([]string, 0, len(commitSteps))
	for _, step := range commitSteps {
		res, err := r.Commit(t.Context(), step)
		if err != nil {
			t.Fatalf("Commit(%q) error = %v", step.Message, err)
		}
		if res.Hash == "" {
			t.Fatalf("Commit(%q) made no commit", step.Message)
		}
		hashes = append(hashes, res.Hash)
	}

	if got := realGit(t, dir, true, "log", "--format=%G?"); got != "G\nG\nG\n" {
		t.Errorf("git log --format=%%G? = %q, want G for all three commits", got)
	}
	for _, h := range hashes {
		realGit(t, dir, false, "verify-commit", h)
	}
	realGit(t, dir, false, "fsck", "--strict")
}

// realGit runs the git command line with the owner's environment, so that it finds the verification
// program. It fails the test when git exits non-zero. Output is returned only when keep is set; otherwise
// it is discarded, because verify-commit prints key ids and identities.
func realGit(t *testing.T, dir string, keep bool, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
	cmd.Stderr = io.Discard
	var out strings.Builder
	if keep {
		cmd.Stdout = &out
	} else {
		cmd.Stdout = io.Discard
	}
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v", args[0], err)
	}
	return out.String()
}
