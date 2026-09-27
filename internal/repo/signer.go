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
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/go-git/x/plugin/objectsigner/program"
)

// signCheckPayload is the message NewSigner signs to prove that the program and the key work.
const signCheckPayload = "git-gen signing check\n"

// statusPrefix starts every status line gpg writes with --status-fd; the arguments of those lines carry
// key ids and fingerprints.
const statusPrefix = "[GNUPG:]"

// Signer signs the encoded form of a git object and returns the signature.
type Signer interface {
	Sign(ctx context.Context, message io.Reader) ([]byte, error)
}

// signerFunc adapts a function to Signer.
type signerFunc func(ctx context.Context, message io.Reader) ([]byte, error)

// Sign calls f.
func (f signerFunc) Sign(ctx context.Context, message io.Reader) ([]byte, error) {
	return f(ctx, message)
}

// NewSigner builds a signer that runs cfg.SigningProgram in the git convention of cfg.SigningFormat, and
// signs a fixed payload once with ctx. It fails when the program cannot be found, when signing fails, or
// when the signature is empty, so that a wrong key or a refused pinentry is reported before any file is
// written.
//
// Errors from signing, here and from the returned Signer, leave out the gpg status lines ("[GNUPG:] ...")
// the program wrote to standard error.
func NewSigner(ctx context.Context, cfg Config) (Signer, error) { //nolint:gocritic // hugeParam: NewSigner runs once per process and contract section 8 fixes Config by value.
	p, err := program.New(program.Format(cfg.SigningFormat), cfg.SigningProgram, cfg.SigningKey)
	if err != nil {
		return nil, fmt.Errorf("signing program: %w", err)
	}
	s := signerFunc(func(ctx context.Context, message io.Reader) ([]byte, error) {
		return sign(ctx, p, message)
	})

	sig, err := s.Sign(ctx, strings.NewReader(signCheckPayload))
	if err != nil {
		return nil, fmt.Errorf("test signature: %w", err)
	}
	if len(bytes.TrimSpace(sig)) == 0 {
		return nil, fmt.Errorf("test signature: %s returned an empty signature", cfg.SigningProgram)
	}
	return s, nil
}

// sign runs s with ctx and removes the gpg status lines from its error.
func sign(ctx context.Context, s Signer, message io.Reader) ([]byte, error) {
	sig, err := s.Sign(ctx, message)
	if err != nil {
		return nil, redact(ctx, err)
	}
	return sig, nil
}

// signError is a signing failure whose text leaves out the gpg status lines. It unwraps only to the
// context's error, so that no caller can reach the original text through errors.Unwrap.
type signError struct {
	text  string
	cause error
}

// Error returns the redacted text.
func (e *signError) Error() string { return e.text }

// Unwrap returns the context's error, or nil when the context had not ended.
func (e *signError) Unwrap() error { return e.cause }

// redact drops everything from a status prefix to the end of its line. The program signer joins the
// program's standard error to its own message with ": ", so the first status line can start mid-line.
func redact(ctx context.Context, err error) error {
	var kept []string
	for line := range strings.SplitSeq(err.Error(), "\n") {
		if before, _, found := strings.Cut(line, statusPrefix); found {
			line = strings.TrimRight(before, ": ")
		}
		if line = strings.TrimSpace(line); line != "" {
			kept = append(kept, line)
		}
	}
	return &signError{text: strings.Join(kept, "; "), cause: ctx.Err()}
}
