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

package boilerplate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ModuleOptions configures [InitModule].
type ModuleOptions struct {
	// Root is the directory that receives go.mod and go.sum. The go command
	// runs there.
	Root string
	// FallbackPath is the module path used when `go mod init` cannot infer one.
	FallbackPath string // "github.com/<org>/<project>"
	// Env is the environment of the go command. As with [exec.Cmd], nil means
	// the environment of the current process.
	Env []string
	// LookPath resolves the go command.
	LookPath func(string) (string, error) // nil means exec.LookPath
}

// ModuleResult reports what [InitModule] did.
type ModuleResult struct {
	Created    bool
	ModulePath string
	GoVersion  string // "1.27"
	Warning    string // set when go.mod was not created and that is not an error
}

// goVersionRE finds the first major.minor after "go" in `go env GOVERSION`,
// which prints forms such as "go1.27.1", "go1.28rc1", "devel go1.28-abcdef" and
// "go1.28-devel_fe515272d0 Fri Sep 25 18:46:22 2026 +0900".
var goVersionRE = regexp.MustCompile(`go(\d+)\.(\d+)`)

// InitModule creates go.mod and an empty go.sum in opts.Root, and does nothing
// when go.mod already exists.
//
// It first reads the toolchain version from `go env GOVERSION`. When the go
// command cannot be found, or the version cannot be read or parsed, it sets
// Warning and creates nothing. Otherwise it runs `go mod init` with no
// argument, which infers the module path inside GOPATH, then
// `go mod init <FallbackPath>` if that fails, then `go mod edit -go=<major.minor>`,
// and finally creates go.sum if there is none.
func InitModule(ctx context.Context, opts ModuleOptions) (ModuleResult, error) {
	gomod := filepath.Join(opts.Root, "go.mod")
	if _, err := os.Lstat(gomod); err == nil {
		return ModuleResult{}, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return ModuleResult{}, fmt.Errorf("boilerplate: %w", err)
	}

	lookPath := opts.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	gobin, err := lookPath("go")
	if err != nil {
		return ModuleResult{Warning: fmt.Sprintf("go.mod not created: %v", err)}, nil
	}

	out, err := runGo(ctx, gobin, opts, "env", "GOVERSION")
	if err != nil {
		if ctx.Err() != nil {
			return ModuleResult{}, fmt.Errorf("boilerplate: %w", err)
		}
		return ModuleResult{Warning: fmt.Sprintf("go.mod not created: %v", err)}, nil
	}
	m := goVersionRE.FindStringSubmatch(out)
	if m == nil {
		return ModuleResult{Warning: fmt.Sprintf("go.mod not created: no major.minor version in `go env GOVERSION` output %q", strings.TrimSpace(out))}, nil
	}
	version := m[1] + "." + m[2]

	if _, err := runGo(ctx, gobin, opts, "mod", "init"); err != nil {
		if _, fallbackErr := runGo(ctx, gobin, opts, "mod", "init", opts.FallbackPath); fallbackErr != nil {
			return ModuleResult{}, fmt.Errorf("boilerplate: %w", errors.Join(err, fallbackErr))
		}
	}

	result := ModuleResult{Created: true, GoVersion: version}
	if _, err := runGo(ctx, gobin, opts, "mod", "edit", "-go="+version); err != nil {
		return result, fmt.Errorf("boilerplate: %w", err)
	}
	content, err := os.ReadFile(gomod)
	if err != nil {
		return result, fmt.Errorf("boilerplate: %w", err)
	}
	result.ModulePath = modulePath(content)

	gosum, err := os.OpenFile(filepath.Join(opts.Root, "go.sum"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("boilerplate: %w", err)
	}
	if err := gosum.Close(); err != nil {
		return result, fmt.Errorf("boilerplate: %w", err)
	}

	return result, nil
}

// runGo runs the go command in opts.Root with opts.Env and returns its
// standard output. The error carries the standard error of the command.
func runGo(ctx context.Context, gobin string, opts ModuleOptions, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, gobin, args...)
	cmd.Dir = opts.Root
	cmd.Env = opts.Env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		err = fmt.Errorf("go %s: %w", strings.Join(args, " "), err)
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			err = fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}

	return string(out), nil
}

// modulePath returns the path of the module directive of a go.mod file, or ""
// when there is none.
func modulePath(gomod []byte) string {
	for line := range strings.Lines(string(gomod)) {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module")
		if !ok || rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
			continue
		}
		rest, _, _ = strings.Cut(rest, "//")
		rest = strings.TrimSpace(rest)
		if unquoted, err := strconv.Unquote(rest); err == nil {
			return unquoted
		}

		return rest
	}

	return ""
}
