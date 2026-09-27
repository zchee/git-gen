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

// Command git-gen initializes a git repository in the working directory.
//
// It writes LICENSE, .gitignore, .gitattributes, CODE_OF_CONDUCT.md and README.md, places the Go
// boilerplate for Go languages, adds the origin remote, makes up to three signed commits and applies
// the repository settings on GitHub.
//
// Usage:
//
//	git-gen [-v] <license> <language>...
//	git-gen -l
package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/charmbracelet/log"

	"github.com/zchee/git-gen/internal/license"
)

// Exit codes of the command.
const (
	exitOK    = 0
	exitFail  = 1
	exitUsage = 2
)

// usageHead is the start of the usage text; the flags and the license names follow it.
const usageHead = `usage: git-gen [-v] <license> <language>...
       git-gen -l

Flags come before the arguments.

`

func main() {
	os.Exit(runMain())
}

// runMain hands the process's arguments, environment, working directory and output streams to run, with a
// context that ends on an interrupt or SIGTERM.
func runMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	wd, err := os.Getwd()
	if err != nil {
		newLogger(os.Stderr, false).Error("Cannot read the working directory", "err", err)
		return exitFail
	}
	return run(ctx, os.Args[1:], os.Getenv, wd, os.Stdout, os.Stderr)
}

// run executes git-gen with args in the absolute directory wd and returns the exit code. It reads
// environment variables through getenv; the go command it starts and the GitHub token lookup see the
// process environment. stdout receives the template list of -l and nothing else; every log line goes to
// stderr.
func run(ctx context.Context, args []string, getenv func(string) string, wd string, stdout, stderr io.Writer) int {
	opts, err := parseArgs(args)
	if errors.Is(err, flag.ErrHelp) {
		printUsage(stderr)
		return exitOK
	}
	if err != nil {
		return usageFailure(stderr, err)
	}

	logger := newLogger(stderr, opts.verbose)
	home := getenv("HOME")
	if home == "" {
		logger.Error("HOME is not set")
		return exitFail
	}
	checkout := filepath.Join(home, "src", "github.com", "github", "gitignore")

	if opts.list {
		err = listTemplates(ctx, logger, stdout, checkout)
	} else {
		n, nameErr := resolveNames(getenv, wd)
		if nameErr != nil {
			return usageFailure(stderr, nameErr)
		}
		g := &generator{
			log:      logger,
			getenv:   getenv,
			dir:      wd,
			home:     home,
			checkout: checkout,
			names:    n,
			license:  opts.license,
			langArgs: opts.langs,
			year:     time.Now().Year(),
		}
		err = g.run(ctx)
	}

	if usageErr, ok := errors.AsType[*usageError](err); ok {
		return usageFailure(stderr, usageErr)
	}
	if err != nil {
		logger.Error(err)
		return exitFail
	}
	return exitOK
}

// options is a parsed command line.
type options struct {
	list    bool
	verbose bool
	license license.License
	langs   []string
}

// usageError is a mistake in the command line. The command prints it with the usage and exits with
// exitUsage.
type usageError struct{ msg string }

// Error returns the description of the mistake.
func (e *usageError) Error() string { return e.msg }

// newFlagSet defines the flags of git-gen on a new flag set that stores into opts and prints nothing.
func newFlagSet(opts *options) *flag.FlagSet {
	fs := flag.NewFlagSet("git-gen", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&opts.list, "l", false, "print the names of the gitignore templates and exit")
	fs.BoolVar(&opts.verbose, "v", false, "log debug messages")
	return fs
}

// parseArgs parses the command line without the program name. It returns flag.ErrHelp for -h and -help
// and a *usageError for any other mistake.
func parseArgs(args []string) (options, error) {
	var opts options
	fs := newFlagSet(&opts)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return options{}, flag.ErrHelp
		}
		return options{}, &usageError{msg: err.Error()}
	}

	rest := fs.Args()
	for _, arg := range rest {
		// The flag package stops at the first argument, so a flag after it would be taken as a language.
		if strings.HasPrefix(arg, "-") {
			return options{}, &usageError{msg: fmt.Sprintf("argument %q starts with '-': flags come before the arguments", arg)}
		}
	}

	if opts.list {
		if len(rest) > 0 {
			return options{}, &usageError{msg: "-l takes no arguments"}
		}
		return opts, nil
	}
	if len(rest) < 2 {
		return options{}, &usageError{msg: "want a license and at least one language"}
	}

	lic, ok := license.Lookup(rest[0])
	if !ok {
		return options{}, &usageError{msg: fmt.Sprintf("unknown license %q", rest[0])}
	}
	opts.license = lic
	opts.langs = rest[1:]
	return opts, nil
}

// printUsage writes the usage text, the flags and the accepted license names to w.
func printUsage(w io.Writer) {
	fs := newFlagSet(new(options))
	fs.SetOutput(w)
	fmt.Fprint(w, usageHead)
	fs.PrintDefaults()
	fmt.Fprintf(w, "\nlicenses: %s\n", strings.Join(license.Names(), ", "))
}

// usageFailure reports a command line mistake with the usage and returns exitUsage.
func usageFailure(stderr io.Writer, err error) int {
	newLogger(stderr, false).Error(err)
	printUsage(stderr)
	return exitUsage
}

// newLogger returns the logger of git-gen: text lines without timestamps at info level, or at debug level
// when verbose is set.
func newLogger(w io.Writer, verbose bool) *log.Logger {
	level := log.InfoLevel
	if verbose {
		level = log.DebugLevel
	}
	return log.NewWithOptions(w, log.Options{Level: level})
}

// names are the organization, project and author the generated files carry.
type names struct {
	org     string
	project string
	author  string
}

// githubNameRE matches the characters GitHub accepts in the name of an organization or a repository.
var githubNameRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// githubNameRule describes a valid organization or project name in an error.
const githubNameRule = `want letters, digits, '.', '-' and '_', and not "." or ".."`

// resolveNames reads ORGANIZATION_NAME, PROJECT_NAME and AUTHOR. An empty variable counts as unset: the
// organization defaults to the name of the parent of wd, the project to the name of wd, and the author to
// the project. The names end up in YAML, .gitignore and .gitattributes lines, so the organization and the
// project must be GitHub names and the author must hold no control character; otherwise the error is a
// *usageError that names the variable, or the directory the value was taken from.
func resolveNames(getenv func(string) string, wd string) (names, error) {
	project, err := githubName(getenv, "PROJECT_NAME", "project", wd)
	if err != nil {
		return names{}, err
	}
	org, err := githubName(getenv, "ORGANIZATION_NAME", "organization", filepath.Dir(wd))
	if err != nil {
		return names{}, err
	}
	author := cmp.Or(getenv("AUTHOR"), project)
	if strings.ContainsFunc(author, unicode.IsControl) {
		return names{}, &usageError{msg: fmt.Sprintf("invalid AUTHOR %q: it contains a control character", author)}
	}
	return names{org: org, project: project, author: author}, nil
}

// githubName returns the value of the variable key or, when it is empty, the name of dir. what says which
// name it is in an error.
func githubName(getenv func(string) string, key, what, dir string) (string, error) {
	valid := func(name string) bool { return githubNameRE.MatchString(name) && name != "." && name != ".." }
	if name := getenv(key); name != "" {
		if !valid(name) {
			return "", &usageError{msg: fmt.Sprintf("invalid %s %q: %s", key, name, githubNameRule)}
		}
		return name, nil
	}
	name := filepath.Base(dir)
	if !valid(name) {
		return "", &usageError{msg: fmt.Sprintf("invalid %s name %q taken from the directory %q: set %s; %s", what, name, dir, key, githubNameRule)}
	}
	return name, nil
}
