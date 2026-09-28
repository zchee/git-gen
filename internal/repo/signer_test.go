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
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-git/x/plugin/objectsigner/program"
	gocmp "github.com/google/go-cmp/cmp"
)

// TestNewSigner checks the preflight signature, which fails when the program fails or signs nothing.
func TestNewSigner(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		cfg      func(t *testing.T) Config
		wantErr  string
		wantIs   error
		wantSign string
	}{
		"success: ok.sh signs the check payload": {
			cfg:      func(t *testing.T) Config { t.Helper(); return stubConfig(t, "ok.sh") },
			wantSign: stubSignature,
		},
		"error: fail.sh exits 1": {
			cfg:     func(t *testing.T) Config { t.Helper(); return stubConfig(t, "fail.sh") },
			wantErr: "test signature",
		},
		"error: the key fail.sh quotes is replaced in the error": {
			cfg: func(t *testing.T) Config {
				t.Helper()
				cfg := stubConfig(t, "fail.sh")
				cfg.SigningKey = "0x0123FEEDFACE4567"
				return cfg
			},
			wantErr: `gpg: skipped "<user.signingkey>": No secret key; gpg: signing failed: No secret key`,
		},
		"error: empty.sh prints no signature": {
			cfg:     func(t *testing.T) Config { t.Helper(); return stubConfig(t, "empty.sh") },
			wantErr: "empty signature",
		},
		"error: the program does not exist": {
			cfg: func(t *testing.T) Config {
				t.Helper()
				cfg := testConfig
				cfg.SigningProgram = testdataPath(t, "signer", "missing.sh")
				return cfg
			},
			wantIs: program.ErrProgramNotFound,
		},
		"error: no signing key": {
			cfg: func(t *testing.T) Config {
				t.Helper()
				cfg := stubConfig(t, "ok.sh")
				cfg.SigningKey = ""
				return cfg
			},
			wantIs: program.ErrEmptySigningKey,
		},
		"error: unknown format": {
			cfg: func(t *testing.T) Config {
				t.Helper()
				cfg := stubConfig(t, "ok.sh")
				cfg.SigningFormat = "pkcs11"
				return cfg
			},
			wantIs: program.ErrUnsupportedFormat,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg := tt.cfg(t)
			s, err := NewSigner(t.Context(), cfg)
			if tt.wantErr != "" || tt.wantIs != nil {
				if err == nil {
					t.Fatal("NewSigner() error = nil, want an error")
				}
				if s != nil {
					t.Errorf("NewSigner() Signer = %v, want nil on error", s)
				}
				if tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("NewSigner() error = %q, want it to contain %q", err, tt.wantErr)
				}
				if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
					t.Errorf("NewSigner() error = %v, want errors.Is(%v)", err, tt.wantIs)
				}
				assertNoStatusLines(t, err)
				assertNoKey(t, err, cfg.SigningKey)
				return
			}
			if err != nil {
				t.Fatalf("NewSigner() error = %v", err)
			}
			sig, err := s.Sign(t.Context(), strings.NewReader("payload\n"))
			if err != nil {
				t.Fatalf("Sign() error = %v", err)
			}
			if diff := gocmp.Diff(tt.wantSign, string(sig)); diff != "" {
				t.Errorf("Sign() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// assertNoStatusLines fails when the error text carries a gpg status line or the made-up key id that
// testdata/signer/fail.sh puts on one.
func assertNoStatusLines(t *testing.T, err error) {
	t.Helper()
	if msg := err.Error(); strings.Contains(msg, statusPrefix) || strings.Contains(msg, stubKeyID) {
		t.Errorf("error text carries a gpg status line: %q", msg)
	}
}

// assertNoKey fails when the error text carries the signing key.
func assertNoKey(t *testing.T, err error, key string) {
	t.Helper()
	if msg := err.Error(); key != "" && strings.Contains(msg, key) {
		t.Errorf("error text carries the signing key %q: %q", key, msg)
	}
}

func TestRedact(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in   string
		key  string
		want string
	}{
		"success: a status line joined to the message is cut at its prefix": {
			in:   "/bin/gpg: running command: exit status 2: [GNUPG:] KEY_CONSIDERED ABCDEF 0\ngpg: signing failed: No secret key\n",
			want: "/bin/gpg: running command: exit status 2; gpg: signing failed: No secret key",
		},
		"success: every status line is dropped wherever it is": {
			in:   "prog: exit status 2: gpg: skipped: No secret key\n[GNUPG:] INV_SGNR 9 ABCDEF\n[GNUPG:] FAILURE sign 17\n",
			want: "prog: exit status 2: gpg: skipped: No secret key",
		},
		"success: an error without status lines is kept": {
			in:   "prog: running command: signal: killed: ",
			want: "prog: running command: signal: killed:",
		},
		"success: an error of status lines only becomes empty": {
			in:   "[GNUPG:] NEWSIG\n[GNUPG:] SIG_CREATED D 1\n",
			want: "",
		},
		"success: every occurrence of the key is replaced": {
			in:   "prog: exit status 2: [GNUPG:] INV_SGNR 9 0xDEADBEEF\ngpg: skipped \"0xDEADBEEF\": No secret key\ngpg: 0xDEADBEEF: signing failed\n",
			key:  "0xDEADBEEF",
			want: "prog: exit status 2; gpg: skipped \"<user.signingkey>\": No secret key; gpg: <user.signingkey>: signing failed",
		},
		"success: a key with spaces and angle brackets is replaced": {
			in:   "gpg: skipped \"Test User <test@example.com>\": No secret key\n",
			key:  "Test User <test@example.com>",
			want: "gpg: skipped \"<user.signingkey>\": No secret key",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := redact(t.Context(), errors.New(tt.in), tt.key)
			if diff := gocmp.Diff(tt.want, err.Error()); diff != "" {
				t.Errorf("redact() mismatch (-want +got):\n%s", diff)
			}
			if errors.Unwrap(err) != nil {
				t.Errorf("redact() unwraps to %v with a live context, want nil", errors.Unwrap(err))
			}
		})
	}
}

func TestRedactKeepsContextError(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := redact(ctx, errors.New("prog: [GNUPG:] KEY_CONSIDERED ABCDEF 0"), "")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("redact() = %v, want errors.Is(context.Canceled)", err)
	}
	assertNoStatusLines(t, err)
}
