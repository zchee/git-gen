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
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

// configLayout is the set of configuration files and variables one test case sees. An empty file content
// means that the file does not exist.
type configLayout struct {
	repo      string // <RepoDir>/.git/config
	gitconfig string // <home>/.gitconfig
	homeXDG   string // <home>/.config/git/config
	envXDG    string // $XDG_CONFIG_HOME/git/config; XDG_CONFIG_HOME is set only when this is non-empty
	global    string // the file GIT_CONFIG_GLOBAL names; the variable is set only when this is non-empty
	env       map[string]string
}

// load writes the layout under fresh temporary directories and runs LoadConfig on it.
func (l configLayout) load(t *testing.T) (Config, error) {
	t.Helper()
	home, repoDir := t.TempDir(), t.TempDir()
	env := map[string]string{}
	files := map[string]string{}
	if l.repo != "" {
		files[filepath.Join(repoDir, ".git", "config")] = l.repo
	}
	if l.gitconfig != "" {
		files[filepath.Join(home, ".gitconfig")] = l.gitconfig
	}
	if l.homeXDG != "" {
		files[filepath.Join(home, ".config", "git", "config")] = l.homeXDG
	}
	if l.envXDG != "" {
		xdg := t.TempDir()
		env["XDG_CONFIG_HOME"] = xdg
		files[filepath.Join(xdg, "git", "config")] = l.envXDG
	}
	if l.global != "" {
		global := filepath.Join(t.TempDir(), "global.gitconfig")
		env["GIT_CONFIG_GLOBAL"] = global
		files[global] = l.global
	}
	for path, content := range files {
		writeFiles(t, filepath.Dir(path), map[string]string{filepath.Base(path): content})
	}
	maps.Copy(env, l.env)
	return LoadConfig(ConfigOptions{Getenv: func(k string) string { return env[k] }, Home: home, RepoDir: repoDir})
}

func TestLoadConfigIdentity(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		layout        configLayout
		wantAuthor    Identity
		wantCommitter Identity
		wantErr       string
	}{
		"success: user.* from the XDG file under the home directory": {
			layout:        configLayout{homeXDG: "[user]\n\tname = Xdg\n\temail = xdg@example.com\n"},
			wantAuthor:    Identity{Name: "Xdg", Email: "xdg@example.com"},
			wantCommitter: Identity{Name: "Xdg", Email: "xdg@example.com"},
		},
		"success: XDG_CONFIG_HOME replaces the XDG file under the home directory": {
			layout: configLayout{
				homeXDG: "[user]\n\tname = HomeXdg\n\temail = home-xdg@example.com\n",
				envXDG:  "[user]\n\tname = EnvXdg\n\temail = env-xdg@example.com\n",
			},
			wantAuthor:    Identity{Name: "EnvXdg", Email: "env-xdg@example.com"},
			wantCommitter: Identity{Name: "EnvXdg", Email: "env-xdg@example.com"},
		},
		"success: ~/.gitconfig beats the XDG file key by key": {
			layout: configLayout{
				gitconfig: "[user]\n\tname = Home\n",
				homeXDG:   "[user]\n\tname = Xdg\n\temail = xdg@example.com\n",
			},
			wantAuthor:    Identity{Name: "Home", Email: "xdg@example.com"},
			wantCommitter: Identity{Name: "Home", Email: "xdg@example.com"},
		},
		"success: the repository file beats the global files": {
			layout: configLayout{
				repo:      "[user]\n\tname = Repo\n",
				gitconfig: "[user]\n\tname = Home\n\temail = home@example.com\n",
			},
			wantAuthor:    Identity{Name: "Repo", Email: "home@example.com"},
			wantCommitter: Identity{Name: "Repo", Email: "home@example.com"},
		},
		"success: GIT_AUTHOR_* beats the repository file for the author only": {
			layout: configLayout{
				repo: "[user]\n\tname = Repo\n\temail = repo@example.com\n",
				env:  map[string]string{"GIT_AUTHOR_NAME": "Env Author", "GIT_AUTHOR_EMAIL": "env-author@example.com"},
			},
			wantAuthor:    Identity{Name: "Env Author", Email: "env-author@example.com"},
			wantCommitter: Identity{Name: "Repo", Email: "repo@example.com"},
		},
		"success: GIT_COMMITTER_* beats committer.* for the committer only": {
			layout: configLayout{
				gitconfig: "[user]\n\tname = User\n\temail = user@example.com\n[committer]\n\tname = Committer\n",
				env:       map[string]string{"GIT_COMMITTER_NAME": "Env Committer"},
			},
			wantAuthor:    Identity{Name: "User", Email: "user@example.com"},
			wantCommitter: Identity{Name: "Env Committer", Email: "user@example.com"},
		},
		"success: author.* and committer.* beat user.*": {
			layout: configLayout{
				gitconfig: "[user]\n\tname = User\n\temail = user@example.com\n" +
					"[author]\n\tname = Author\n\temail = author@example.com\n" +
					"[committer]\n\tname = Committer\n\temail = committer@example.com\n",
			},
			wantAuthor:    Identity{Name: "Author", Email: "author@example.com"},
			wantCommitter: Identity{Name: "Committer", Email: "committer@example.com"},
		},
		"success: author.name in a global file beats user.name in the repository file": {
			layout: configLayout{
				repo:    "[user]\n\tname = Repo\n\temail = repo@example.com\n",
				homeXDG: "[author]\n\tname = Global Author\n",
			},
			wantAuthor:    Identity{Name: "Global Author", Email: "repo@example.com"},
			wantCommitter: Identity{Name: "Repo", Email: "repo@example.com"},
		},
		"success: an empty author.name falls back to user.name": {
			layout: configLayout{
				gitconfig: "[author]\n\tname =\n[user]\n\tname = User\n\temail = user@example.com\n",
			},
			wantAuthor:    Identity{Name: "User", Email: "user@example.com"},
			wantCommitter: Identity{Name: "User", Email: "user@example.com"},
		},
		"success: GIT_CONFIG_GLOBAL replaces ~/.gitconfig and the XDG file": {
			layout: configLayout{
				gitconfig: "[user]\n\tname = Home\n\temail = home@example.com\n",
				homeXDG:   "[user]\n\tname = Xdg\n\temail = xdg@example.com\n",
				global:    "[user]\n\tname = Global\n\temail = global@example.com\n",
			},
			wantAuthor:    Identity{Name: "Global", Email: "global@example.com"},
			wantCommitter: Identity{Name: "Global", Email: "global@example.com"},
		},
		"success: the last value within one file wins, section and key names ignore case": {
			layout: configLayout{
				gitconfig: "[user]\n\tname = First\n\temail = first@example.com\n[core]\n\tbare = false\n[User]\n\tNAME = Last\n",
			},
			wantAuthor:    Identity{Name: "Last", Email: "first@example.com"},
			wantCommitter: Identity{Name: "Last", Email: "first@example.com"},
		},
		"error: GIT_CONFIG_GLOBAL hides an e-mail set only in ~/.gitconfig": {
			layout: configLayout{
				gitconfig: "[user]\n\temail = home@example.com\n",
				global:    "[user]\n\tname = Global\n",
			},
			wantErr: "author e-mail is not set",
		},
		"error: GIT_AUTHOR_* alone does not name the committer": {
			layout: configLayout{
				env: map[string]string{"GIT_AUTHOR_NAME": "Env Author", "GIT_AUTHOR_EMAIL": "env-author@example.com"},
			},
			wantErr: "committer name is not set",
		},
		"error: no name anywhere": {
			layout:  configLayout{gitconfig: "[user]\n\temail = home@example.com\n"},
			wantErr: "author name is not set",
		},
		"error: no e-mail anywhere": {
			layout:  configLayout{gitconfig: "[user]\n\tname = Home\n"},
			wantErr: "author e-mail is not set",
		},
		"error: no configuration file at all": {
			layout:  configLayout{},
			wantErr: "author name is not set",
		},
		"error: an empty user.name in the repository file hides the global one": {
			layout: configLayout{
				repo:      "[user]\n\tname =\n",
				gitconfig: "[user]\n\tname = Home\n\temail = home@example.com\n",
			},
			wantErr: "author name is not set",
		},
		"error: malformed file": {
			layout:  configLayout{gitconfig: "[user\n\tname = Home\n"},
			wantErr: "parse git config",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg, err := tt.layout.load(t)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("LoadConfig() error = %v, want an error containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			if diff := gocmp.Diff(tt.wantAuthor, cfg.Author); diff != "" {
				t.Errorf("Author mismatch (-want +got):\n%s", diff)
			}
			if diff := gocmp.Diff(tt.wantCommitter, cfg.Committer); diff != "" {
				t.Errorf("Committer mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestLoadConfigDefaultBranch(t *testing.T) {
	t.Parallel()

	const identity = "[user]\n\tname = U\n\temail = u@example.com\n"
	tests := map[string]struct {
		layout configLayout
		want   string
	}{
		"success: main when init.defaultBranch is unset": {
			layout: configLayout{gitconfig: identity},
			want:   "main",
		},
		"success: init.defaultBranch from a global file": {
			layout: configLayout{gitconfig: identity + "[init]\n\tdefaultBranch = trunk\n"},
			want:   "trunk",
		},
		"success: the repository file beats the global file, key names ignore case": {
			layout: configLayout{
				repo:      "[init]\n\tdefaultbranch = develop\n",
				gitconfig: identity + "[init]\n\tdefaultBranch = trunk\n",
			},
			want: "develop",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg, err := tt.layout.load(t)
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			if cfg.DefaultBranch != tt.want {
				t.Errorf("DefaultBranch = %q, want %q", cfg.DefaultBranch, tt.want)
			}
		})
	}
}

func TestLoadConfigSigning(t *testing.T) {
	t.Parallel()

	fixture := Identity{Name: "Fixture User", Email: "fixture@example.com"}
	tests := map[string]struct {
		fixture string
		want    Config
		wantErr string
	}{
		"success: openpgp defaults to gpg and the committer as the key": {
			fixture: "openpgp-default.gitconfig",
			want: Config{
				SigningFormat: "openpgp", SigningProgram: "gpg",
				SigningKey: "Fixture User <fixture@example.com>",
			},
		},
		"success: openpgp uses gpg.program and user.signingkey": {
			fixture: "openpgp-program.gitconfig",
			want: Config{
				SigningFormat: "openpgp", SigningProgram: "/opt/fixture/bin/gpg2",
				SigningKey: "0x0123456789ABCDEF",
			},
		},
		"success: gpg.openpgp.program beats gpg.program": {
			fixture: "openpgp-subsection.gitconfig",
			want: Config{
				SigningFormat: "openpgp", SigningProgram: "/opt/fixture/bin/gpg-openpgp",
				SigningKey: "fixture@example.com",
			},
		},
		"success: the last gpg.program within one file wins": {
			fixture: "openpgp-last-wins.gitconfig",
			want: Config{
				SigningFormat: "openpgp", SigningProgram: "/opt/fixture/bin/second",
				SigningKey: "Fixture User <fixture@example.com>",
			},
		},
		"success: ssh uses gpg.ssh.program, never gpg.program": {
			fixture: "ssh.gitconfig",
			want: Config{
				SigningFormat: "ssh", SigningProgram: "/opt/fixture/bin/ssh-keygen",
				SigningKey: "~/.ssh/id_ed25519.pub",
			},
		},
		"success: ssh defaults to ssh-keygen": {
			fixture: "ssh-default-program.gitconfig",
			want: Config{
				SigningFormat: "ssh", SigningProgram: "ssh-keygen",
				SigningKey: "key::ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFixtureFixtureFixtureFixtureFixture fixture",
			},
		},
		"success: x509 uses gpg.x509.program and the committer as the key": {
			fixture: "x509.gitconfig",
			want: Config{
				SigningFormat: "x509", SigningProgram: "/opt/fixture/bin/gpgsm-wrapper",
				SigningKey: "Fixture User <fixture@example.com>",
			},
		},
		"success: x509 defaults to gpgsm": {
			fixture: "x509-default-program.gitconfig",
			want: Config{
				SigningFormat: "x509", SigningProgram: "gpgsm",
				SigningKey: "0xFEDCBA9876543210",
			},
		},
		"success: init.defaultBranch from the fixture": {
			fixture: "identity.gitconfig",
			want: Config{
				DefaultBranch: "trunk", SigningFormat: "openpgp", SigningProgram: "gpg",
				SigningKey: "Fixture User <fixture@example.com>",
			},
		},
		"error: ssh without user.signingkey": {
			fixture: "ssh-no-key.gitconfig",
			wantErr: "user.signingkey is not set",
		},
		"error: unknown gpg.format": {
			fixture: "unknown-format.gitconfig",
			wantErr: `unsupported gpg.format "pkcs11"`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env := map[string]string{"GIT_CONFIG_GLOBAL": testdataPath(t, "gitconfig", tt.fixture)}
			cfg, err := LoadConfig(ConfigOptions{Getenv: func(k string) string { return env[k] }, Home: t.TempDir()})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("LoadConfig() error = %v, want an error containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			want := tt.want
			want.Author, want.Committer = fixture, fixture
			if want.DefaultBranch == "" {
				want.DefaultBranch = "main"
			}
			if diff := gocmp.Diff(want, cfg); diff != "" {
				t.Errorf("LoadConfig() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestLoadConfigNilGetenv(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeFiles(t, home, map[string]string{".gitconfig": "[user]\n\tname = Home\n\temail = home@example.com\n"})
	cfg, err := LoadConfig(ConfigOptions{Home: home})
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if diff := gocmp.Diff(Identity{Name: "Home", Email: "home@example.com"}, cfg.Author); diff != "" {
		t.Errorf("Author mismatch (-want +got):\n%s", diff)
	}
}

// TestLoadConfigSymlink is AC28: go-git's own loader fails on a path through a symlink whose target is
// absolute (plan F4); LoadConfig must read it.
func TestLoadConfigSymlink(t *testing.T) {
	t.Parallel()

	const content = "[user]\n\tname = Linked\n\temail = linked@example.com\n"
	tests := map[string]struct {
		setup func(t *testing.T, home, target string) map[string]string
	}{
		"success: ~/.config is a symlink with an absolute target": {
			setup: func(t *testing.T, home, target string) map[string]string {
				t.Helper()
				writeFiles(t, target, map[string]string{"git/config": content})
				if err := os.Symlink(target, filepath.Join(home, ".config")); err != nil {
					t.Fatal(err)
				}
				return nil
			},
		},
		"success: GIT_CONFIG_GLOBAL goes through a symlinked directory": {
			setup: func(t *testing.T, _, target string) map[string]string {
				t.Helper()
				writeFiles(t, target, map[string]string{"global.gitconfig": content})
				link := filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
				return map[string]string{"GIT_CONFIG_GLOBAL": filepath.Join(link, "global.gitconfig")}
			},
		},
		"success: XDG_CONFIG_HOME is a symlink to a symlink": {
			setup: func(t *testing.T, _, target string) map[string]string {
				t.Helper()
				writeFiles(t, target, map[string]string{"git/config": content})
				first := filepath.Join(t.TempDir(), "first")
				second := filepath.Join(t.TempDir(), "second")
				if err := os.Symlink(target, first); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(first, second); err != nil {
					t.Fatal(err)
				}
				return map[string]string{"XDG_CONFIG_HOME": second}
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			home, target := t.TempDir(), t.TempDir()
			if !filepath.IsAbs(target) {
				t.Fatalf("target %q is not absolute", target)
			}
			env := tt.setup(t, home, target)
			cfg, err := LoadConfig(ConfigOptions{Getenv: func(k string) string { return env[k] }, Home: home})
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			if diff := gocmp.Diff(Identity{Name: "Linked", Email: "linked@example.com"}, cfg.Author); diff != "" {
				t.Errorf("Author mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestLoadConfigRepoDir(t *testing.T) {
	t.Parallel()

	const global = "[user]\n\tname = Global\n\temail = global@example.com\n"
	tests := map[string]struct {
		setup   func(t *testing.T, repoDir string)
		want    Identity
		wantErr string
	}{
		"success: a working tree without .git is skipped": {
			setup: func(*testing.T, string) {},
			want:  Identity{Name: "Global", Email: "global@example.com"},
		},
		"success: a .git file instead of a directory is skipped": {
			setup: func(t *testing.T, repoDir string) {
				t.Helper()
				writeFiles(t, repoDir, map[string]string{".git": "gitdir: /nonexistent\n"})
			},
			want: Identity{Name: "Global", Email: "global@example.com"},
		},
		"error: .git/config is a directory": {
			setup: func(t *testing.T, repoDir string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(repoDir, ".git", "config"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "parse git config",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			home, repoDir := t.TempDir(), t.TempDir()
			writeFiles(t, home, map[string]string{".gitconfig": global})
			tt.setup(t, repoDir)
			cfg, err := LoadConfig(ConfigOptions{Getenv: func(string) string { return "" }, Home: home, RepoDir: repoDir})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("LoadConfig() error = %v, want an error containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			if diff := gocmp.Diff(tt.want, cfg.Author); diff != "" {
				t.Errorf("Author mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
