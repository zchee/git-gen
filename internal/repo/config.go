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
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/go-git/gcfg/v2"
	"github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
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
	// SigningKeyFromCommitter is true when user.signingkey is unset and SigningKey is the committer's
	// "name <email>".
	SigningKeyFromCommitter bool
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

// configFile is one parsed configuration file.
type configFile struct {
	raw *format.Config
	// openpgpSubsection is "openpgp" when gpg.openpgp.program occurs after the last gpg.program in the
	// file, and "" otherwise. git reads the two keys as one setting.
	openpgpSubsection string
}

// configFiles holds the parsed configuration files, highest precedence first.
type configFiles []*configFile

// programKeys are the keys that name a signing program: the subsection of the gpg section, and the name
// of the key.
var programKeys = []struct{ subsection, name string }{
	{"", "gpg.program"},
	{"openpgp", "gpg.openpgp.program"},
	{"ssh", "gpg.ssh.program"},
	{"x509", "gpg.x509.program"},
}

// LoadConfig resolves the identities, the default branch and the signing setup. Each warning names a key
// of the repository's file that was ignored.
//
// The files are read in this order of precedence, highest first: the .git/config of opts.RepoDir; then
// the file named by GIT_CONFIG_GLOBAL alone, or else ~/.gitconfig followed by $XDG_CONFIG_HOME/git/config
// (~/.config/git/config when XDG_CONFIG_HOME is empty). The files after the repository's file are the
// global files. A missing file is skipped. Each key is taken from the first file that sets it; within one
// file the last value wins. [include] and includeIf are not followed. The system configuration and the
// GIT_CONFIG_COUNT variables are not read.
//
// The author is GIT_AUTHOR_NAME and GIT_AUTHOR_EMAIL, then author.name and author.email, then user.name and
// user.email; the committer likewise uses GIT_COMMITTER_* and committer.*. A missing name or e-mail is an
// error. An init.defaultBranch that is not a valid branch name is an error.
//
// The signing program is read from the global files only, so that a repository cannot choose a program
// to run; a program key in the repository's file is ignored with a warning. It is gpg.openpgp.program or
// gpg.program, whichever git reads last, or "gpg" for the openpgp format (the default), gpg.ssh.program or
// "ssh-keygen" for ssh, and gpg.x509.program or "gpgsm" for x509. The key is user.signingkey; without it
// the committer's "name <email>" is used, except for ssh, where it is an error.
func LoadConfig(opts ConfigOptions) (cfg Config, warnings []string, err error) {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}

	var repoFiles configFiles
	if opts.RepoDir != "" {
		path := filepath.Join(opts.RepoDir, ".git", "config")
		if repoFiles, err = readConfigFiles([]string{path}); err != nil {
			return Config{}, nil, err
		}
		for _, f := range repoFiles {
			for _, k := range programKeys {
				if _, ok := lookup(f.raw, "gpg", k.subsection, "program"); ok {
					warnings = append(warnings, fmt.Sprintf("%s: %s is ignored: the signing program is read from the global git configuration only", path, k.name))
				}
			}
		}
	}
	globals, err := readConfigFiles(globalConfigPaths(opts, getenv))
	if err != nil {
		return Config{}, nil, err
	}
	files := slices.Concat(repoFiles, globals)

	author, err := files.identity(getenv, "author")
	if err != nil {
		return Config{}, nil, err
	}
	committer, err := files.identity(getenv, "committer")
	if err != nil {
		return Config{}, nil, err
	}

	cfg = Config{
		Author:        author,
		Committer:     committer,
		DefaultBranch: cmp.Or(files.get("init", "", "defaultBranch"), defaultBranch),
		SigningFormat: cmp.Or(files.get("gpg", "", "format"), string(program.FormatOpenPGP)),
	}
	// Open would fail on the name only after it created .git, which a later run cannot open.
	if err := plumbing.NewBranchReferenceName(cfg.DefaultBranch).Validate(); err != nil {
		return Config{}, nil, fmt.Errorf("init.defaultBranch %q is not a valid branch name: %w", cfg.DefaultBranch, err)
	}

	switch program.Format(cfg.SigningFormat) {
	case program.FormatOpenPGP:
		cfg.SigningProgram = cmp.Or(globals.openpgpProgram(), "gpg")
	case program.FormatSSH:
		cfg.SigningProgram = cmp.Or(globals.get("gpg", "ssh", "program"), "ssh-keygen")
	case program.FormatX509:
		cfg.SigningProgram = cmp.Or(globals.get("gpg", "x509", "program"), "gpgsm")
	default:
		return Config{}, nil, fmt.Errorf("unsupported gpg.format %q: want openpgp, ssh or x509", cfg.SigningFormat)
	}

	cfg.SigningKey = files.get("user", "", "signingkey")
	if cfg.SigningKey == "" {
		if program.Format(cfg.SigningFormat) == program.FormatSSH {
			return Config{}, nil, errors.New("gpg.format is ssh but user.signingkey is not set")
		}
		cfg.SigningKey = committer.Name + " <" + committer.Email + ">"
		cfg.SigningKeyFromCommitter = true
	}

	return cfg, warnings, nil
}

// globalConfigPaths lists the global configuration files in order of precedence, highest first.
func globalConfigPaths(opts ConfigOptions, getenv func(string) string) []string {
	if global := getenv("GIT_CONFIG_GLOBAL"); global != "" {
		return []string{global}
	}
	var paths []string
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
		f, err := readConfigFile(path)
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			continue
		}
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}

func readConfigFile(path string) (*configFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read git config: %w", err)
	}
	// config.ReadConfig keeps the order of keys within a section or a subsection, but not between the two,
	// so the file is also read key by key for the order of the OpenPGP program keys.
	file := &configFile{}
	err = gcfg.ReadWithCallback(bytes.NewReader(data), func(section, subsection, key, _ string, _ bool) error {
		if key != "" && strings.EqualFold(section, "gpg") && strings.EqualFold(key, "program") && (subsection == "" || subsection == "openpgp") {
			file.openpgpSubsection = subsection
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("parse git config %s: %w", path, err)
	}
	cfg, err := config.ReadConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parse git config %s: %w", path, err)
	}
	file.raw = cfg.Raw
	return file, nil
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
	for _, f := range files {
		if v, ok := lookup(f.raw, section, subsection, key); ok {
			return v
		}
	}
	return ""
}

// openpgpProgram returns the OpenPGP signing program, or "" when no file sets one. As in git, gpg.program
// and gpg.openpgp.program are one setting: the first file that sets either decides, and within that file
// the key that occurs last.
func (files configFiles) openpgpProgram() string {
	for _, f := range files {
		if v, ok := lookup(f.raw, "gpg", f.openpgpSubsection, "program"); ok {
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
