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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/log"

	"github.com/zchee/git-gen/internal/boilerplate"
	"github.com/zchee/git-gen/internal/github"
	"github.com/zchee/git-gen/internal/gitignore"
	"github.com/zchee/git-gen/internal/license"
	"github.com/zchee/git-gen/internal/repo"
)

const (
	// contact replaces "[INSERT CONTACT METHOD]" in CODE_OF_CONDUCT.md.
	contact = "zchee.io@gmail.com"
	// gitignoreURL is cloned into the checkout when the checkout does not exist.
	gitignoreURL = "https://github.com/github/gitignore.git"
	// syncTimeout bounds the clone or pull of the checkout.
	syncTimeout = 60 * time.Second
	// githubTimeout bounds the requests to the GitHub API.
	githubTimeout = 10 * time.Second
	// spdxApache is the license whose Go scaffold includes hack/boilerplate/boilerplate.go.txt.
	spdxApache = "Apache-2.0"
	// fileMode is the permission of the files git-gen writes itself.
	fileMode fs.FileMode = 0o644
)

// syncCheckout clones the github/gitignore checkout in dir when it does not exist and, when pull is set,
// pulls the main branch into it. Only a failed clone is an error: without the checkout there are no
// templates. Any other failure leaves the checkout as it is and is logged as a warning.
func syncCheckout(ctx context.Context, logger *log.Logger, dir string, pull bool) error {
	ctx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()

	action, err := repo.Sync(ctx, dir, gitignoreURL, pull)
	if err != nil {
		if action == repo.SyncCloned {
			return fmt.Errorf("gitignore checkout: %w", err)
		}
		logger.Warn("Cannot update the gitignore checkout; using it as it is", "dir", dir, "err", err)
		return nil
	}

	switch action {
	case repo.SyncCloned:
		logger.Info("Cloned the gitignore checkout", "url", gitignoreURL, "dir", dir)
	case repo.SyncPulled:
		logger.Debug("Pulled the gitignore checkout", "dir", dir)
	case repo.SyncUpToDate:
		logger.Debug("The gitignore checkout is up to date", "dir", dir)
	case repo.SyncSkippedNotRepository:
		logger.Debug("The gitignore checkout is not a git repository; sync skipped", "dir", dir)
	case repo.SyncSkippedNoPull:
		logger.Debug("The gitignore checkout exists; not pulled", "dir", dir)
	default:
		logger.Warn("Unknown result of the gitignore checkout sync", "dir", dir, "action", int(action))
	}
	return nil
}

// listTemplates writes the template names of the checkout in dir to stdout, one per line. It clones the
// checkout when it does not exist but never pulls.
func listTemplates(ctx context.Context, logger *log.Logger, stdout io.Writer, dir string) error {
	if err := syncCheckout(ctx, logger, dir, false); err != nil {
		return err
	}
	catalog, err := gitignore.Open(dir)
	if err != nil {
		return fmt.Errorf("list templates: %w", err)
	}
	defer catalog.Close()

	templates, err := catalog.List()
	if err != nil {
		return fmt.Errorf("list templates: %w", err)
	}
	var b strings.Builder
	for _, name := range templates {
		b.WriteString(name)
		b.WriteString("\n")
	}
	if _, err := io.WriteString(stdout, b.String()); err != nil {
		return fmt.Errorf("list templates: %w", err)
	}
	return nil
}

// generator creates or completes one repository in dir.
type generator struct {
	log      *log.Logger
	getenv   func(string) string
	dir      string
	home     string
	checkout string
	names    names
	license  license.License
	langArgs []string
	year     int
}

// prepared is everything the generator writes and signs with, worked out before the first write.
type prepared struct {
	langs     []gitignore.Language
	scaffold  gitignore.Scaffold
	gitignore []byte
	files     []boilerplate.File
	config    repo.Config
	signer    repo.Signer
}

// run syncs the checkout, prepares every file and the signer, and only then initializes the repository,
// writes the files, adds origin, commits, and applies the GitHub settings.
func (g *generator) run(ctx context.Context) error {
	if err := syncCheckout(ctx, g.log, g.checkout, true); err != nil {
		return err
	}
	p, err := g.preflight(ctx)
	if err != nil {
		return err
	}

	r, existed, err := repo.Open(repo.Options{Dir: g.dir, Config: p.config, Signer: p.signer})
	if err != nil {
		return fmt.Errorf("repository: %w", err)
	}
	if existed {
		g.log.Warn(".git directory already exists")
	}

	if err := g.writeFiles(ctx, p); err != nil {
		return err
	}
	origin := g.addOrigin(r)
	if err := g.commit(ctx, r); err != nil {
		return err
	}
	g.applyGitHub(ctx, origin)

	g.log.Infof("Generated %s repository", g.names.project)
	return nil
}

// preflight resolves the languages, checks and renders the templates, reads the git configuration and
// signs a test payload. It writes nothing into the working directory.
func (g *generator) preflight(ctx context.Context) (*prepared, error) {
	catalog, err := gitignore.Open(g.checkout)
	if err != nil {
		return nil, fmt.Errorf("gitignore templates: %w", err)
	}
	defer catalog.Close()

	langs, err := g.resolveLanguages(catalog)
	if err != nil {
		return nil, err
	}
	p := &prepared{langs: langs, scaffold: gitignore.Scaffolds(langs)}

	if p.files, err = g.renderBoilerplate(p.scaffold); err != nil {
		return nil, err
	}
	content, warnings, err := catalog.Compose(g.names.author, langs)
	if err != nil {
		return nil, fmt.Errorf("compose .gitignore: %w", err)
	}
	for _, w := range warnings {
		g.log.Warn(w)
	}
	p.gitignore = content

	// This check covers both the read of the working directory's git configuration and the later Open.
	if err := repo.CheckDir(g.dir); err != nil {
		return nil, fmt.Errorf("repository: %w", err)
	}
	if p.config, err = g.loadConfig(); err != nil {
		return nil, err
	}
	text, err := g.license.Render(license.Vars{Author: g.names.author, Owner: p.config.Author.Name, Year: g.year})
	if err != nil {
		return nil, fmt.Errorf("render LICENSE: %w", err)
	}
	if text != nil {
		p.files = append(p.files, boilerplate.File{Path: "LICENSE", Content: text, Mode: fileMode})
	}

	if p.signer, err = repo.NewSigner(ctx, p.config); err != nil {
		return nil, fmt.Errorf("signing: %w", err)
	}
	return p, nil
}

// resolveLanguages resolves the language arguments. An argument without a template is dropped with a
// warning; a name that could leave the checkout is a command line mistake.
func (g *generator) resolveLanguages(catalog *gitignore.Catalog) ([]gitignore.Language, error) {
	langs, unknown, err := catalog.Resolve(g.langArgs)
	if invalid, ok := errors.AsType[*gitignore.InvalidNameError](err); ok {
		return nil, &usageError{msg: fmt.Sprintf("invalid language %q: want a template name inside the gitignore checkout", invalid.Name)}
	}
	if err != nil {
		return nil, fmt.Errorf("resolve languages: %w", err)
	}
	for _, name := range unknown {
		g.log.Warn("No gitignore template for the language; skipped", "language", name)
	}
	return langs, nil
}

// renderBoilerplate checks that every template the scaffold needs exists, then renders them.
func (g *generator) renderBoilerplate(scaffold gitignore.Scaffold) ([]boilerplate.File, error) {
	isGo := scaffold&gitignore.ScaffoldGo != 0
	set := boilerplate.Set{
		Go:       isGo,
		Makefile: scaffold&gitignore.ScaffoldMakefile != 0,
		Hack:     isGo && g.license.SPDXID == spdxApache,
	}
	dir := boilerplate.Dir(g.getenv, g.home)
	if missing := boilerplate.Missing(dir, set); len(missing) > 0 {
		return nil, fmt.Errorf("missing boilerplate templates: %s", strings.Join(missing, ", "))
	}

	files, warnings, err := boilerplate.Render(dir, set, boilerplate.Vars{
		Author:       g.names.author,
		Organization: g.names.org,
		Project:      g.names.project,
		Contact:      contact,
		SPDXID:       g.license.SPDXID,
		Year:         g.year,
	})
	if err != nil {
		return nil, fmt.Errorf("render boilerplate: %w", err)
	}
	for _, w := range warnings {
		g.log.Warn(w)
	}
	return files, nil
}

// loadConfig reads the git configuration and logs the identity and signing setup it found.
func (g *generator) loadConfig() (repo.Config, error) {
	cfg, warnings, err := repo.LoadConfig(repo.ConfigOptions{Getenv: g.getenv, Home: g.home, RepoDir: g.dir})
	if err != nil {
		return repo.Config{}, fmt.Errorf("git configuration: %w", err)
	}
	for _, w := range warnings {
		g.log.Warn(w)
	}
	key := "user.signingkey"
	if cfg.SigningKeyFromCommitter {
		key = "committer identity"
	}
	g.log.Debug("Git identity",
		"author", cfg.Author.Name+" <"+cfg.Author.Email+">",
		"committer", cfg.Committer.Name+" <"+cfg.Committer.Email+">",
		"branch", cfg.DefaultBranch)
	g.log.Debug("Signing", "format", cfg.SigningFormat, "program", cfg.SigningProgram, "key", key)
	return cfg, nil
}

// writeFiles creates the files that are absent, rewrites .gitignore, adds the missing blocks to
// .gitattributes and, for a Go scaffold, creates go.mod.
func (g *generator) writeFiles(ctx context.Context, p *prepared) error {
	written, skipped, err := boilerplate.Write(g.dir, p.files)
	for _, name := range written {
		g.log.Debug("Created", "path", name)
	}
	for _, name := range skipped {
		g.log.Debug("Exists; left as it is", "path", name)
	}
	if err != nil {
		return fmt.Errorf("write files: %w", err)
	}

	root, err := os.OpenRoot(g.dir)
	if err != nil {
		return fmt.Errorf("write files: %w", err)
	}
	defer root.Close()

	if err := root.WriteFile(".gitignore", p.gitignore, fileMode); err != nil {
		return fmt.Errorf("write .gitignore: %w", err)
	}
	existing, err := root.ReadFile(".gitattributes")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read .gitattributes: %w", err)
	}
	if attrs := gitignore.Attributes(existing, g.names.author, p.langs); !bytes.Equal(attrs, existing) {
		if err := root.WriteFile(".gitattributes", attrs, fileMode); err != nil {
			return fmt.Errorf("write .gitattributes: %w", err)
		}
	}

	if p.scaffold&gitignore.ScaffoldGo != 0 {
		return g.initModule(ctx)
	}
	return nil
}

// initModule creates go.mod and go.sum unless go.mod exists. A missing go command is a warning, and so is a
// module path other than github.com/<org>/<project>, which go mod init takes from GOPATH or from an import
// comment in the directory.
func (g *generator) initModule(ctx context.Context) error {
	want := "github.com/" + g.names.org + "/" + g.names.project
	res, err := boilerplate.InitModule(ctx, boilerplate.ModuleOptions{Root: g.dir, FallbackPath: want})
	switch {
	case err != nil:
		return fmt.Errorf("go.mod: %w", err)
	case res.Warning != "":
		g.log.Warn(res.Warning)
	case res.Created:
		g.log.Debug("Created go.mod", "module", res.ModulePath, "go", res.GoVersion)
		if res.ModulePath != want {
			g.log.Warn("go.mod declares a module path other than github.com/<org>/<project>", "module", res.ModulePath, "want", want)
		}
	default:
		g.log.Debug("Exists; left as it is", "path", "go.mod")
	}
	return nil
}

// addOrigin adds the origin remote unless it exists, and returns the URL origin ends up with. A failure is
// a warning and returns "".
func (g *generator) addOrigin(r *repo.Repository) string {
	url := "git@github.com:" + g.names.org + "/" + g.names.project + ".git"
	origin, added, err := r.AddRemote("origin", url)
	switch {
	case err != nil:
		g.log.Warn("Cannot add remote origin", "url", url, "err", err)
		return ""
	case added:
		g.log.Debug("Added remote origin", "url", origin)
	default:
		g.log.Debug("Remote origin exists; left as it is", "url", origin)
	}
	return origin
}

// commitSteps returns the commits git-gen makes, in order. go.mod and go.sum are staged even when
// .gitignore ignores them.
func commitSteps() []repo.CommitStep {
	return []repo.CommitStep{
		{Message: "Initial commit", Paths: []string{".gitignore", ".gitattributes", "LICENSE", "CODE_OF_CONDUCT.md"}},
		{Message: "github: add .github directory", Paths: []string{".github/PULL_REQUEST_TEMPLATE.md"}},
		{Message: "go.mod: init module", Paths: []string{"go.mod", "go.sum"}, Force: true},
	}
}

// commit makes the commits of commitSteps. A step with nothing added makes no commit. An ignored path is
// left out with a warning that names it.
func (g *generator) commit(ctx context.Context, r *repo.Repository) error {
	for i, step := range commitSteps() {
		res, err := r.Commit(ctx, step)
		for _, name := range res.Skipped {
			g.log.Warn("Not committed: ignored by .gitignore", "path", name)
		}
		if err != nil {
			return fmt.Errorf("commit step %d: %w", i+1, err)
		}
		if res.Hash == "" {
			g.log.Debug("Nothing to commit", "message", step.Message)
			continue
		}
		g.log.Debug("Committed", "commit", res.Hash, "message", step.Message)
	}
	return nil
}

// applyGitHub applies the repository settings on GitHub. Every outcome other than success is logged as a
// warning; none of them fails the run.
func (g *generator) applyGitHub(ctx context.Context, origin string) {
	ctx, cancel := context.WithTimeout(ctx, githubTimeout)
	defer cancel()

	want := g.names.org + "/" + g.names.project
	outcome, err := github.New(github.Options{}).Apply(ctx, origin, g.names.org, g.names.project)
	if err != nil {
		g.log.Warn("GitHub settings not applied", "repository", want, "err", err)
		return
	}
	switch outcome {
	case github.Applied:
		g.log.Info("Applied the GitHub settings", "repository", want)
	case github.SkippedNoToken:
		g.log.Warn("GitHub settings skipped: no GitHub token", "repository", want)
	case github.SkippedNotGitHub:
		g.log.Warn("GitHub settings skipped: origin is not a github.com repository", "origin", origin)
	case github.SkippedOriginMismatch:
		g.log.Warn("GitHub settings skipped: origin points to another repository", "origin", origin, "want", want)
	case github.SkippedNotFound:
		g.log.Warn("GitHub settings skipped: the repository does not exist on GitHub or the token cannot see it", "repository", want)
	case github.Failed:
		g.log.Warn("GitHub settings not applied", "repository", want)
	default:
		g.log.Warn("Unknown result of the GitHub settings", "repository", want, "outcome", int(outcome))
	}
}
