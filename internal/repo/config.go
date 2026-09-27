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
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/go-git/go-git/v6/config"
	format "github.com/go-git/go-git/v6/plumbing/format/config"
	"github.com/go-git/x/plugin/objectsigner/program"
)

// defaultBranch is the branch a new repository starts on when init.defaultBranch is unset.
const defaultBranch = "main"

// Identity is the name and e-mail address of a commit author or committer.
type Identity struct{ Name, Email string }

// Config is the part of the git configuration that git-gen uses.
type Config struct {
	Author         Identity
	Committer      Identity
	DefaultBranch  string // "main" when unset
	SigningFormat  string // "openpgp", "ssh" or "x509"
	SigningProgram string
	SigningKey     string
}

// ConfigOptions tells LoadConfig where to look.
type ConfigOptions struct {
	// Getenv looks up an environment variable. Nil means an empty environment.
	// An empty value is treated as unset.
	Getenv func(string) string
	// Home is the user's home directory. Empty means that no file under it is read.
	Home string
	// RepoDir is a working tree whose .git/config is read when present.
	RepoDir string
}

// configFiles holds the parsed configuration files, highest precedence first.
type configFiles []*format.Config

// LoadConfig resolves the identities, the default branch and the signing setup.
//
// The files are read in this order of precedence, highest first: the .git/config of opts.RepoDir; then
// the file named by GIT_CONFIG_GLOBAL alone, or else ~/.gitconfig followed by $XDG_CONFIG_HOME/git/config
// (~/.config/git/config when XDG_CONFIG_HOME is empty). A missing file is skipped. Each key is taken from
// the first file that sets it; within one file the last value wins. [include] and includeIf are not
// followed. The system configuration and the GIT_CONFIG_COUNT variables are not read.
//
// The author is GIT_AUTHOR_NAME and GIT_AUTHOR_EMAIL, then author.name and author.email, then user.name and
// user.email; the committer likewise uses GIT_COMMITTER_* and committer.*. A missing name or e-mail is an
// error.
//
// The signing program is gpg.openpgp.program, gpg.program or "gpg" for the openpgp format (the default),
// gpg.ssh.program or "ssh-keygen" for ssh, and gpg.x509.program or "gpgsm" for x509. The key is
// user.signingkey; without it the committer's "name <email>" is used, except for ssh, where it is an
// error.
func LoadConfig(opts ConfigOptions) (Config, error) {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}

	files, err := readConfigFiles(configPaths(opts, getenv))
	if err != nil {
		return Config{}, err
	}

	author, err := files.identity(getenv, "author")
	if err != nil {
		return Config{}, err
	}
	committer, err := files.identity(getenv, "committer")
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		Author:        author,
		Committer:     committer,
		DefaultBranch: cmp.Or(files.get("init", "", "defaultBranch"), defaultBranch),
		SigningFormat: cmp.Or(files.get("gpg", "", "format"), string(program.FormatOpenPGP)),
	}

	switch program.Format(cfg.SigningFormat) {
	case program.FormatOpenPGP:
		cfg.SigningProgram = cmp.Or(files.get("gpg", "openpgp", "program"), files.get("gpg", "", "program"), "gpg")
	case program.FormatSSH:
		cfg.SigningProgram = cmp.Or(files.get("gpg", "ssh", "program"), "ssh-keygen")
	case program.FormatX509:
		cfg.SigningProgram = cmp.Or(files.get("gpg", "x509", "program"), "gpgsm")
	default:
		return Config{}, fmt.Errorf("unsupported gpg.format %q: want openpgp, ssh or x509", cfg.SigningFormat)
	}

	cfg.SigningKey = files.get("user", "", "signingkey")
	if cfg.SigningKey == "" {
		if program.Format(cfg.SigningFormat) == program.FormatSSH {
			return Config{}, errors.New("gpg.format is ssh but user.signingkey is not set")
		}
		cfg.SigningKey = committer.Name + " <" + committer.Email + ">"
	}

	return cfg, nil
}

// configPaths lists the configuration files in order of precedence, highest first.
func configPaths(opts ConfigOptions, getenv func(string) string) []string {
	var paths []string
	if opts.RepoDir != "" {
		paths = append(paths, filepath.Join(opts.RepoDir, ".git", "config"))
	}
	if global := getenv("GIT_CONFIG_GLOBAL"); global != "" {
		return append(paths, global)
	}
	if opts.Home != "" {
		paths = append(paths, filepath.Join(opts.Home, ".gitconfig"))
	}
	if xdg := getenv("XDG_CONFIG_HOME"); xdg != "" {
		paths = append(paths, filepath.Join(xdg, "git", "config"))
	} else if opts.Home != "" {
		paths = append(paths, filepath.Join(opts.Home, ".config", "git", "config"))
	}
	return paths
}

// readConfigFiles opens each file with the os package, which follows symlinks with absolute targets that
// go-git's own loader rejects, and parses it with config.ReadConfig.
func readConfigFiles(paths []string) (configFiles, error) {
	files := make(configFiles, 0, len(paths))
	for _, path := range paths {
		raw, err := readConfigFile(path)
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			continue
		}
		if err != nil {
			return nil, err
		}
		files = append(files, raw)
	}
	return files, nil
}

func readConfigFile(path string) (*format.Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read git config: %w", err)
	}
	defer f.Close()

	cfg, err := config.ReadConfig(f)
	if err != nil {
		return nil, fmt.Errorf("parse git config %s: %w", path, err)
	}
	return cfg.Raw, nil
}

// identity resolves the author or the committer, as role says.
func (files configFiles) identity(getenv func(string) string, role string) (Identity, error) {
	env := "GIT_" + strings.ToUpper(role) + "_"
	id := Identity{
		Name:  cmp.Or(getenv(env+"NAME"), files.get(role, "", "name"), files.get("user", "", "name")),
		Email: cmp.Or(getenv(env+"EMAIL"), files.get(role, "", "email"), files.get("user", "", "email")),
	}
	if id.Name == "" {
		return Identity{}, fmt.Errorf("%s name is not set: set user.name, %s.name or %sNAME", role, role, env)
	}
	if id.Email == "" {
		return Identity{}, fmt.Errorf("%s e-mail is not set: set user.email, %s.email or %sEMAIL", role, role, env)
	}
	return id, nil
}

// get returns the value of a key from the first file that sets it, or "" when none does.
func (files configFiles) get(section, subsection, key string) string {
	for _, raw := range files {
		if v, ok := lookup(raw, section, subsection, key); ok {
			return v
		}
	}
	return ""
}

// lookup finds a key in one file. It does not use format.Config.Section, which appends an empty section
// when the name is missing. Section and key names match case-insensitively, subsection names exactly, and
// a later occurrence wins over an earlier one, as in git.
func lookup(raw *format.Config, section, subsection, key string) (string, bool) {
	for _, s := range slices.Backward(raw.Sections) {
		if !s.IsName(section) {
			continue
		}
		if subsection == "" {
			if s.HasOption(key) {
				return s.Option(key), true
			}
			continue
		}
		for _, ss := range slices.Backward(s.Subsections) {
			if ss.IsName(subsection) && ss.HasOption(key) {
				return ss.Option(key), true
			}
		}
	}
	return "", false
}
