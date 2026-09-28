# git-gen

[![codecov.io][codecov-badge]][codecov]

git-gen turns the working directory into a new git repository in one run. It writes `LICENSE`, `.gitignore`,
`.gitattributes`, `CODE_OF_CONDUCT.md` and `README.md`, places the Go boilerplate when a Go language is named, adds
the `origin` remote, makes up to three signed commits, and applies a fixed set of repository settings on GitHub.
git-gen is a Go port of the shell script `git-gen`; the differences are listed in
[Differences from the script](#differences-from-the-script).

## Install

```sh
go install github.com/zchee/git-gen@latest
```

git-gen needs Go 1.27 or later to build.

## Requirements

### The gitignore checkout

The `.gitignore` sections come from a checkout of [github/gitignore](https://github.com/github/gitignore) at
`$HOME/src/github.com/github/gitignore`. `HOME` must be set.

| State of the checkout | What git-gen does |
|---|---|
| It does not exist | Clones `https://github.com/github/gitignore.git` into it. If the clone fails, the run ends with exit code 1. |
| It is a git repository | Pulls `main` from its `origin` remote; only a fast-forward is possible. If the pull fails, git-gen logs a warning and uses the checkout as it is. |
| It exists but is not a git repository | Uses it as it is. |

The clone or pull must finish within 60 seconds. `git-gen -l` clones a missing checkout but never pulls.

### The boilerplate directory

The boilerplate directory is `$XDG_CONFIG_HOME/boilerplate`, or `~/.config/boilerplate` when `XDG_CONFIG_HOME` is
unset or empty. These files must exist in it, depending on the arguments:

| File in the boilerplate directory | Needed when | Placed as |
|---|---|---|
| `.github/CODE_OF_CONDUCT.md` | always | `CODE_OF_CONDUCT.md` |
| `.github/ISSUE_TEMPLATE/bug_report.yml` | the languages include `go`, `Go` or `community/Golang` | `.github/ISSUE_TEMPLATE/bug_report.yml` |
| `go/.golangci.yaml` | the languages include `go`, `Go` or `community/Golang` | `.golangci.yaml` |
| `go/Makefile` | the languages include `Go` or `community/Golang` | `Makefile` |
| `go/boilerplate.go.txt` | the languages include `go`, `Go` or `community/Golang`, and the license is `apache2` or `Apache2` | `hack/boilerplate/boilerplate.go.txt` |

When a needed file is missing, git-gen exits with code 1 before it writes anything into the working directory. The
message names every missing file:

```text
ERRO missing boilerplate templates: /home/me/.config/boilerplate/go/Makefile
```

With `go`, `Go` or `community/Golang`, every other regular file under `.github/` is placed at the same path, for
example `.github/PULL_REQUEST_TEMPLATE.md`, which the second commit takes. `.github/CODE_OF_CONDUCT.md` goes to the
top level instead, `.DS_Store` files are skipped, and no empty directory is created. A placed file gets the permission
bits of its template, less those the umask clears.

A symbolic link in the boilerplate directory is followed only while its target stays inside the directory; a link to
a regular file then counts as that file. A template that is a link leading out of the directory ends the run with
exit code 1 before anything is written, and the error names the template and ends in `path escapes from parent`. The
boilerplate directory itself may be reached through a symbolic link, for example when `~/.config/boilerplate` is one.

git-gen replaces these placeholders and copies everything else unchanged:

| Template | Placeholder | Replaced with |
|---|---|---|
| `.github/CODE_OF_CONDUCT.md` | `[INSERT CONTACT METHOD]` | `zchee.io@gmail.com` |
| `.github/ISSUE_TEMPLATE/bug_report.yml` | `PKG` as a whole word | the project name |
| `go/.golangci.yaml` | the `github.com/` of `prefix(github.com/)`, of `module-path: github.com/`, and of the `- github.com/` item under `local-prefixes:` | `github.com/<org>/<project>` |
| `go/boilerplate.go.txt` | `AUTHOR` and `YEAR` as whole words, and `LICENSE_IDENTIFIER` | the author, the current year, and the SPDX ID of the license |

When a placeholder other than `LICENSE_IDENTIFIER` is not found, git-gen logs a warning such as
`WARN template <path>: required rule "PKG" matched nothing` and places the file anyway. `<org>`, `<project>` and the
author are described in [Environment variables](#environment-variables).

### Commit signing

Every commit git-gen makes is signed, whatever `commit.gpgSign` says. git-gen reads the signing setup from the git
configuration and signs a test payload before it writes anything into the working directory. If the program cannot
be found, fails, or returns an empty signature, the run ends with exit code 1 and the working directory is left as it
was. The program is run the way git runs it, so it may ask for a passphrase.

| `gpg.format` | Signing program | Key |
|---|---|---|
| `openpgp` (the default) | `gpg.program` or `gpg.openpgp.program`, whichever git reads last, else `gpg` | `user.signingkey`, else the committer's `name <email>` |
| `ssh` | `gpg.ssh.program`, else `ssh-keygen` | `user.signingkey`; without it the run ends with exit code 1 |
| `x509` | `gpg.x509.program`, else `gpgsm` | `user.signingkey`, else the committer's `name <email>` |

Any other `gpg.format` ends the run with exit code 1. `gpg.program` and `gpg.openpgp.program` are one setting, as in
git: of the two, the one that git reads last wins.

The four program keys, `gpg.program`, `gpg.openpgp.program`, `gpg.ssh.program` and `gpg.x509.program`, are read from
the global configuration files only, so that a repository cannot choose the program git-gen runs. When the
`.git/config` of the working directory `<dir>` sets one of them, git-gen ignores it and logs one warning per key:

```text
WARN <dir>/.git/config: gpg.program is ignored: the signing program is read from the global git configuration only
```

`gpg.format`, `user.signingkey`, the identity keys and `init.defaultBranch` are read from `.git/config` as well, which
takes precedence over the global files.

A signing error leaves out the `[GNUPG:]` status lines that the program printed and shows `<user.signingkey>` in
place of the key, as in `gpg: skipped "<user.signingkey>": No secret key`.

The author and the committer must have a name and an e-mail address:

| Identity | Sources, first match wins |
|---|---|
| Author | `GIT_AUTHOR_NAME` and `GIT_AUTHOR_EMAIL`, then `author.name` and `author.email`, then `user.name` and `user.email` |
| Committer | `GIT_COMMITTER_NAME` and `GIT_COMMITTER_EMAIL`, then `committer.name` and `committer.email`, then `user.name` and `user.email` |

The configuration files git-gen reads are listed in [Differences from git](#differences-from-git).

### The go command

For `go`, `Go` and `community/Golang`, git-gen creates `go.mod` with the `go` command found on `PATH`, and an empty
`go.sum`, unless `go.mod` exists:

1. `go env GOVERSION` gives the major and minor version, for example `1.27`.
2. `go mod init` runs without an argument, which takes the module path from the directory's place under `GOPATH` or
   from an import comment in the directory. If it fails, `go mod init github.com/<org>/<project>` runs instead.
3. `go mod edit -go=<major.minor>` sets the `go` line.
4. git-gen creates an empty `go.sum` when there is none.

When the module path of the new `go.mod` is not `github.com/<org>/<project>`, git-gen keeps it and logs a warning.
For the directory `$GOPATH/src/example.com/foo/bar`:

```text
WARN go.mod declares a module path other than github.com/<org>/<project> module=example.com/foo/bar want=github.com/foo/bar
```

The `go` command runs in the working directory with the environment of git-gen. When `go` is not on `PATH`, or its
version cannot be read, git-gen logs a warning that starts with `WARN go.mod not created:`, creates neither file, and
the run continues without the third commit. When both `go mod init` commands fail, or `go mod edit` fails, the run
ends with exit code 1. The files are already written at that point, and no commit has been made.

An existing `go.mod` is left as it is, and no `go.sum` is created next to it. When that `go.mod` is not yet
committed, the third commit takes it alone.

### A GitHub token (optional)

The GitHub step needs a token for `github.com`. git-gen looks for it in this order: `GH_TOKEN`, `GITHUB_TOKEN`, the
token stored in the `gh` configuration, and finally the output of `gh auth token`. The lookup counts against the
10 second limit of the GitHub step. Without a token the step is skipped with a warning and the run still succeeds.
git-gen does not create the repository on GitHub.

## Usage

```text
git-gen [-v] <license> <language>...
git-gen -l
```

| Flag | Meaning |
|---|---|
| `-l` | Print the names of the gitignore templates that are regular files, one per line, to stdout and exit. It takes no arguments. |
| `-v` | Log debug messages. |
| `-h`, `-help` | Print the usage, the flags and the accepted license names to stderr and exit with code 0. |

Flags come before the arguments. The first argument is the license and every later argument is a language, so
`git-gen apache2 go -v` is an error: `argument "-v" starts with '-': flags come before the arguments`. An argument
that starts with `-` is rejected after `--` as well.

All log lines go to stderr. stdout carries only the list that `-l` prints. The default level prints warnings, errors
and a few `INFO` lines; `-v` adds `DEBU` lines. A signing error leaves out the `[GNUPG:]` status lines that the
signing program printed and shows `<user.signingkey>` in place of the key.

Create a repository in `~/src/github.com/acme/rocket` with the Apache 2.0 license and the Go boilerplate. With a
gitignore checkout that git-gen can update, `go` on `PATH` and no GitHub token, the run prints:

```sh
mkdir -p ~/src/github.com/acme/rocket
cd ~/src/github.com/acme/rocket
git-gen apache2 go
```

```text
WARN GitHub settings skipped: no GitHub token repository=acme/rocket
INFO Generated rocket repository
```

With `-v`, the same run also prints `DEBU` lines such as these:

```text
DEBU Git identity author="Jane Doe <jane@example.com>" committer="Jane Doe <jane@example.com>" branch=main
DEBU Signing format=openpgp program=gpg key="committer identity"
DEBU Added remote origin url=git@github.com:acme/rocket.git
```

`key=` says where the key came from, `user.signingkey` or `committer identity`, and does not print the key.

List the templates of the checkout:

```sh
git-gen -l
```

```text
Global/JetBrains
Go
Python
Rust
community/Golang
```

The list holds the regular `*.gitignore` files at most three levels deep, named by their path relative to the
checkout without the extension and cut to two path elements. Templates that are symbolic links, such as `Clojure`,
are not listed, but can be named as languages. A real checkout lists many more names than this example.

## Licenses

| Name | SPDX ID | Copyright line in `LICENSE` |
|---|---|---|
| `bsd`, `bsd-project` | `BSD-3-Clause` | `Copyright (c) <year> The <author> Authors.` |
| `bsd-owner` | `BSD-3-Clause` | `Copyright (c) <year> <author name>.` |
| `bsd-go` | `BSD-3-Clause` | `Copyright (c) <year> The Go Authors.` |
| `mit`, `mit-project` | `MIT` | `Copyright (c) <year> The <author> Authors` |
| `mit-owner` | `MIT` | `Copyright (c) <year> <author name>` |
| `apache2`, `Apache2` | `Apache-2.0` | none; the text is written as published |
| `CC4`, `CC-BY-SA-4.0` | `CC-BY-SA-4.0` | none; the text is written as published |
| `none` | none | no `LICENSE` is written |

`<year>` is the current year, `<author>` is the value described under
[Environment variables](#environment-variables), and `<author name>` is the name of the git author identity. The text
is the SPDX License List text embedded in git-gen (see [License data](#license-data)); the line that holds the
copyright placeholder is replaced as a whole. The Apache 2.0 text keeps its appendix line
`Copyright [yyyy] [name of copyright owner]`.

Names are case-sensitive: `MIT` and `APACHE2` are unknown. An unknown license ends the run with exit code 2 and the
usage lists the accepted names.

## Languages

Each language argument adds one section to `.gitignore`, headed `# github/gitignore/<template>`. These names have
fixed rules:

| Argument | Template | Changes to the section | Files placed |
|---|---|---|---|
| `go` | `Go.gitignore` | Drops the first two lines. Uncomments `vendor/`, removes the `# Go workspace file` block (`go.work`, `go.work.sum`) and the `# env file` block (`.env`). Appends a block of patterns for object files, `_obj`, `_test`, architecture-specific files, cgo output, `_testmain.go`, `old.txt`, `new.txt`, `bench.txt` and `*.pprof`. | The Go files, and the `go.sum` block of `.gitattributes` |
| `Go` | `Go.gitignore` | Same as `go`. | The Go files, `Makefile`, and the `go.sum` block of `.gitattributes` |
| `community/Golang` | the single `*.AllowList.gitignore` in `community/Golang/` | Drops the comment lines at the top and the blank line after them. Replaces `# But not these files...` with `!/.gitattributes` and `# !Makefile` with `!Makefile`, and removes the comment `# ...even if they are in subdirectories`. | The Go files and `Makefile` |
| `go-pkg`, `go-simple` | `Go.gitignore` | Drops the first two lines. No other change. | nothing |
| `Rust` | `Rust.gitignore` | Drops the first two lines. Removes the lines from `# RustRover` through `#.idea/` and the blank line before them. | nothing |

The Go files are the files under `.github/`, `.golangci.yaml`, `go.mod` and `go.sum`, plus
`hack/boilerplate/boilerplate.go.txt` when the license is `apache2` or `Apache2`. When several languages are named,
the files placed are the union of their files, placed once.

Any other name resolves to `<name>.gitignore` in the checkout, or else to the single `*.AllowList.gitignore` directly
inside the directory `<name>`. No line is dropped from its section except trailing blank lines, and nothing else in
it is changed: `Python`, `Global/JetBrains`. Names are case-sensitive and every path element must match an entry of
the checkout exactly, also on a case-insensitive file system, so `golang`, `rust` and `python` match nothing. A name
without a template is skipped with a warning and the run goes on:

```text
WARN No gitignore template for the language; skipped language=golang
```

A name that is empty, absolute or contains a `..` element ends the run with exit code 2. A directory that holds more
than one `*.AllowList.gitignore` ends it with exit code 1.

Naming a template twice gives one section. When several arguments share a template, the section gets the changes of
the first of them that has any, so `go-pkg go` gives the changed `Go` section. When a change does not find its text,
git-gen skips that change and logs a warning. This holds for every change in the table, each change of
`community/Golang` included:

```text
WARN github/gitignore/<template>: cannot <change>: text not found, section left as it is
```

## What a run does

1. **Sync the checkout**, as described in [The gitignore checkout](#the-gitignore-checkout).
2. **Prepare everything, writing nothing into the working directory.** git-gen resolves the languages, checks and
   renders the boilerplate templates, composes `.gitignore`, checks `.git` (see step 3), reads the git configuration,
   renders `LICENSE`, and signs the test payload. A failure here leaves the working directory as it was.
3. **Initialize the repository** unless `.git` exists; then it logs `WARN .git directory already exists` and opens
   the repository instead. A new repository starts on `init.defaultBranch`, or `main` when it is unset. A value that
   is not a valid branch name ends the run with exit code 1 in step 2.

   The check of step 2 runs before the git configuration is read. An existing `.git` must be a directory: a `.git`
   file, as in a linked worktree or a submodule, or a symbolic link ends the run with exit code 1. On Unix, `.git`
   and `.git/config` must also be owned by the user running git-gen, or the run ends with exit code 1. There is no
   allow list like git's `safe.directory`.

   ```text
   ERRO repository: <dir>/.git is a file, not a directory: linked worktrees and submodules are not supported
   ERRO repository: <dir>/.git is not a directory
   ERRO repository: <path> is owned by uid <n>, not by the current user (uid <m>)
   ```

4. **Write the files.**

   | File | Rule |
   |---|---|
   | `LICENSE`, `README.md`, `CODE_OF_CONDUCT.md`, the files under `.github/`, `.golangci.yaml`, `Makefile`, `hack/boilerplate/boilerplate.go.txt` | Created only when the path does not exist. An existing path is left untouched. |
   | `go.mod`, `go.sum` | Only when `go.mod` does not exist, `go.mod` is created by the `go` command and an empty `go.sum` by git-gen; see [The go command](#the-go-command). An existing `go.mod` is left untouched and no `go.sum` is added next to it. |
   | `.gitignore` | Always rewritten. |
   | `.gitattributes` | Keeps its content and gets only the blocks it lacks. |

   `README.md` holds one line, `# <project>`. `.gitignore` starts with this header and continues with one section per
   template:

   ```text
   # <author> project generated files to ignore
   #  If you want to ignore files created by your editor/tools,
   #  please consider a global .gitignore https://docs.github.com/en/get-started/git-basics/ignoring-files.
   #  PLEASE DO NOT open a pull request to add something created by your editor or tools
   ```

   `.gitattributes` gets the header block below, and, for `go` and `Go`, the `go.sum` block after a blank line. Each
   block is appended only when it is not already present as whole lines; a header block that names another author
   counts as absent.

   ```text
   # <author> project gitattributes file
   #  https://github.com/github-linguist/linguist/blob/main/docs/overrides.md

   * text=auto eol=lf
   ```

   ```text
   go.sum       linguist-vendored
   go.work.sum  linguist-vendored
   ```

5. **Add the `origin` remote** `git@github.com:<org>/<project>.git`, unless a remote named `origin` exists, which is
   left as it is. A failure is logged as a warning.
6. **Commit** in three steps:

   | Step | Message | Paths |
   |---|---|---|
   | 1 | `Initial commit` | `.gitignore`, `.gitattributes`, `LICENSE`, `CODE_OF_CONDUCT.md` |
   | 2 | `github: add .github directory` | `.github/PULL_REQUEST_TEMPLATE.md` |
   | 3 | `go.mod: init module` | `go.mod`, `go.sum` |

   Each step stages the paths that exist. In steps 1 and 2, a path that is not tracked yet and that the working
   tree's `.gitignore` ignores is left out with a warning; a tracked path is staged even when it is ignored, as
   `git add` does. Step 3 stages `go.mod` and `go.sum` even when they are ignored:

   ```text
   WARN Not committed: ignored by .gitignore path=CODE_OF_CONDUCT.md
   ```

   git-gen reads a `.gitignore` with CRLF line ends or a UTF-8 byte order mark as git does.

   A step makes a commit only when the index then holds at least one newly added file, as
   `git status --porcelain | grep '^A'` would show. The commit takes everything staged, including files staged
   before the run. Every commit is signed and carries the author and committer of the git configuration. When the
   signing program returns an empty signature, no commit is written and the run ends with exit code 1; each commit
   is also read back, and one without a signature ends the run the same way. The other placed files, such as
   `README.md`, `.golangci.yaml`, `Makefile`, `hack/` and the rest of `.github/`, stay untracked.

   With `community/Golang`, the allowlist ignores `CODE_OF_CONDUCT.md` and `.github/PULL_REQUEST_TEMPLATE.md`, so a
   run in an empty directory makes only the `Initial commit` and `go.mod: init module` commits.
7. **Apply the GitHub settings.** git-gen looks up the token, reads the repository `<org>/<project>` through the
   GitHub REST API and then changes six settings. The token lookup and the requests must finish within 10 seconds
   together. No outcome of this step fails the run:

   | Setting | Value |
   |---|---|
   | `allow_update_branch` | `true` |
   | `delete_branch_on_merge` | `true` |
   | `allow_auto_merge` | `true` |
   | `allow_merge_commit` | `false` |
   | `allow_rebase_merge` | `false` |
   | `has_wiki` | `false` |

   | Log line | Cause |
   |---|---|
   | `INFO Applied the GitHub settings repository=<org>/<project>` | GitHub accepted the settings. |
   | `WARN GitHub settings skipped: no GitHub token repository=<org>/<project>` | No token was found. No request was sent. |
   | `WARN GitHub settings skipped: origin is not a github.com repository origin=<url>` | The URL of `origin` is not a `github.com` URL, or `origin` could not be added. No request was sent. |
   | `WARN GitHub settings skipped: origin points to another repository origin=<url> want=<org>/<project>` | `origin` names a repository other than `<org>/<project>`, compared without regard to case. No request was sent. |
   | `WARN GitHub settings skipped: the repository does not exist on GitHub or the token cannot see it repository=<org>/<project>` | GitHub answered 404 when git-gen read the repository. |
   | `WARN GitHub settings not applied repository=<org>/<project> err=<error>` | Any other failure, including a 404 to the change of the settings. When the token lookup does not finish within the 10 seconds, `<error>` ends in `context deadline exceeded` and no request is sent. |

8. **Log** `INFO Generated <project> repository`.

## Environment variables

An empty value counts as unset.

| Variable | Default | Used for |
|---|---|---|
| `HOME` | none; the run ends with exit code 1 when it is unset | The gitignore checkout, `~/.gitconfig`, and the defaults that start with `~` |
| `ORGANIZATION_NAME` | the name of the parent of the working directory | `<org>`: the `origin` URL, the GitHub repository, the fallback module path and `.golangci.yaml` |
| `PROJECT_NAME` | the name of the working directory | `<project>`: the same places as `<org>`, plus `README.md`, `bug_report.yml` and the last log line |
| `AUTHOR` | the project name | The headers of `.gitignore` and `.gitattributes`, the `The <author> Authors` copyright line, and `boilerplate.go.txt` |
| `XDG_CONFIG_HOME` | `~/.config` | The boilerplate directory, the git configuration file `git/config` and the `gh` configuration |
| `GIT_CONFIG_GLOBAL` | unset | When set, the only global git configuration file that is read |
| `GIT_AUTHOR_NAME`, `GIT_AUTHOR_EMAIL` | unset | The author of the commits and the `<author name>` of `bsd-owner` and `mit-owner` |
| `GIT_COMMITTER_NAME`, `GIT_COMMITTER_EMAIL` | unset | The committer of the commits |
| `GH_TOKEN`, `GITHUB_TOKEN` | unset | The GitHub token |
| `GH_CONFIG_DIR` | unset | When set, the `gh` configuration directory, in place of `$XDG_CONFIG_HOME/gh` or `~/.config/gh` |
| `GH_PATH` | unset | When set, the `gh` program that the token lookup runs, in place of the one on `PATH` |
| `PATH` | inherited | Where `go`, the signing program and `gh` are found |

For the working directory `~/src/github.com/acme/rocket`, `<org>` is `acme`, `<project>` is `rocket`, and the author
is `rocket`.

The organization and the project, whether set or taken from a directory name, must consist of ASCII letters, digits,
`.`, `-` and `_`, and must not be `.` or `..`. The author must not contain a control character. Otherwise the run
ends with exit code 2 before the checkout is synced, and the message names the variable, or the directory and the
variable to set:

```text
ERRO invalid ORGANIZATION_NAME "<value>": want letters, digits, '.', '-' and '_', and not "." or ".."
ERRO invalid project name "<name>" taken from the directory "<dir>": set PROJECT_NAME; want letters, digits, '.', '-' and '_', and not "." or ".."
ERRO invalid AUTHOR "<value>": it contains a control character
```

`git-gen -l` does not read these names.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | The run succeeded, or `-h` printed the usage. Skipped languages and a skipped or failed GitHub step do not change the code. |
| 1 | The run failed, for example: `HOME` is not set, the checkout could not be cloned, a boilerplate template is missing or is a link that leads out of the boilerplate directory, `.git` is not a directory or is owned by another user, the git configuration is incomplete or names an unsupported `gpg.format` or an invalid `init.defaultBranch`, signing failed, `go mod init` or `go mod edit` failed, or a file could not be written. The error is logged with `ERRO`. |
| 2 | The command line is wrong: too few arguments, an unknown flag or license, a flag after the arguments, arguments with `-l`, an invalid language name, or an invalid organization, project or author. The error and the usage go to stderr and nothing is written into the working directory. |

## Running it again

git-gen can run again in a directory it has already set up, with the same or other arguments:

- It logs `WARN .git directory already exists` and opens the repository; it never initializes it again.
- Existing `LICENSE`, `README.md`, `CODE_OF_CONDUCT.md`, `go.mod` and Go files stay as they are. Missing ones are
  created, except `go.sum`, which is created only together with a new `go.mod`.
- `.gitignore` is rewritten from the current arguments and the current checkout.
- `.gitattributes` gets only the blocks it lacks, so a second run with the same arguments leaves it unchanged.
- An existing `origin` stays as it is.
- A path that an earlier run committed is staged again even when the new `.gitignore` ignores it, without a warning.
- A commit step makes a commit only when the index then holds a newly added file. With the same arguments, an
  unchanged checkout and no new file staged before the run, a second run makes no commit.
- The GitHub settings are applied again.

A step that only modifies tracked files makes no commit, because it adds no new file. Its changes stay staged, and
go into the next step that adds a file. For example, `git-gen apache2 Rust` followed by `git-gen apache2 go` in the
same directory: in the second run, step 1 stages the modified `.gitignore` and `.gitattributes` without committing,
and step 2 commits them together with `.github/PULL_REQUEST_TEMPLATE.md`. When no later step adds a file, the changes
stay staged and uncommitted.

A run that ended with an error after it wrote files can be run again: the files it wrote are kept, and the commit
steps take what is not yet committed. An empty signature leaves no commit behind, so the next run makes that commit.

## Differences from the script

| Topic | The script | git-gen |
|---|---|---|
| `LICENSE` source | Copied `boilerplate/license/<type>` and filled it with `sed`. `mit` and `mit-project` failed because `MIT_PROJECT` did not exist. | Writes the SPDX License List text embedded in git-gen; `boilerplate/license/` is not read. Every license name works. |
| `LICENSE` layout | The layout of the boilerplate files. | The SPDX layout: the Apache 2.0 text is not indented, except items (a) to (d) of section 4 and the paragraph after them, paragraphs of the BSD, Apache 2.0 and CC-BY-SA texts are not wrapped, the BSD conditions are numbered `1.` to `3.`, the BSD text has no `All rights reserved.` line, and the MIT title is `MIT License`. |
| Copyright line | `bsd` wrote `Copyright (c) <year>, The <author> Authors`. The owner licenses named the person fixed in the file. A `$YEAR` token became `$` followed by the year. | See [Licenses](#licenses): for example `Copyright (c) <year> The <author> Authors.` The owner licenses name the git author. |
| Names | Used `ORGANIZATION_NAME`, `PROJECT_NAME` and `AUTHOR`, or the directory names, as given. | Rejects, with exit code 2, an organization or project name that holds characters other than ASCII letters, digits, `.`, `-` and `_`, and an author that holds a control character. |
| Lowercase language names | On a case-insensitive file system, as on macOS, `rust` and `python` found `Rust.gitignore` and `Python.gitignore`, and the header named the argument, such as `# github/gitignore/go`. | Names match exactly: `rust` and `python` are skipped with a warning. `go` still works through the language table. The header names the template: `# github/gitignore/Go`. |
| `go mod init` failure | Ignored; the run went on without `go.mod`. | Retries with `github.com/<org>/<project>`; if that fails too, the run ends with exit code 1. |
| `go` line of `go.mod` | Always `go 1.27`. | The major and minor version of `go env GOVERSION`. Without `go`, a warning and no `go.mod`. |
| `go.mod` without `go.sum` | With an existing `go.mod` and no `go.sum`, `git add -f go.mod go.sum` failed and the script stopped before the `go.mod: init module` commit. | Commits `go.mod` alone. |
| Command line mistakes | Exit code 1; with no arguments, an unbound variable error. The usage went to stdout. | Exit code 2. The error and the usage go to stderr. |
| Log output | `[INFO]` and `[WARN]` on stdout. | `INFO`, `WARN`, `ERRO` and, with `-v`, `DEBU` lines on stderr. stdout carries only the `-l` list. |
| Unknown language | Warned, then appended a section with the previous template's body, or an empty section (after a `file: unbound variable` message) when no template came before it. | Warns and skips it. |
| Top lines of a template | Dropped the first two lines of every template, so `Python.gitignore` lost `__pycache__/`. | Drops lines only for the languages in the [language table](#languages); other templates are kept whole, except trailing blank lines. |
| `Rust` | Deleted lines 22 to 28 of `.gitignore`, which was right only when `Rust` came first. | Removes the `# RustRover` block by its content and warns when it is not found. |
| `community/Golang` | The section started in the middle of a comment. The allowlist ignored `CODE_OF_CONDUCT.md`, so `git add` failed and no commit was made. | Drops the whole leading comment. Ignored paths are left out of the commits with a warning, and the other commits are made. |
| `.gitattributes` | Appended its blocks on every run, so they repeated. | Appends only the blocks it lacks. |
| Files that exist | Overwrote `CODE_OF_CONDUCT.md`, `.golangci.yaml`, `Makefile` and `hack/` on every run. `cp -R` created `.github/.github` when `.github` existed. | Creates each file only when it is absent and never overwrites. |
| `.github/` copy | Copied `.DS_Store` files and the empty `workflows/` directory. | Skips `.DS_Store` and creates no empty directory. |
| `.golangci.yaml` | Its two substitutions matched nothing in the current template. | Replaces three `github.com/` placeholders with `github.com/<org>/<project>`. |
| `XDG_CONFIG_HOME` unset | Failed after `git init`. | Uses `~/.config`. |
| Offline | A failed `git pull` of the checkout stopped the run. | Warns and uses the checkout as it is. Only a failed clone of a missing checkout is an error. |
| Signing failure | `git commit --gpg-sign` failed after all files were written. | A test signature fails the run before anything is written into the working directory. |
| Git hooks and templates | `git init` applied `init.templateDir`, and `git commit` ran the hooks, including those under `core.hooksPath`. | Applies no template directory and runs no hook. |
| GitHub settings | `gh repo view` and `gh repo edit` on the repository `gh` resolved; a failure of `gh repo edit` ended the run with a non-zero code. | Calls the GitHub REST API only when `origin` is `<org>/<project>` on github.com; every failure is a warning and the exit code stays 0. |
| External programs | Needed `git`, GNU `sed` (`sed -i` and `gsed`) and `gh`. | Runs no `git`, `sed` or `gsed`. It runs the signing program and `go`, and runs `gh auth token` only when no other source has a token. |

## Differences from git

git-gen does not run the `git` command. It reads the git configuration and `.gitignore` itself, and covers less than
git does:

- **Configuration files.** git-gen reads the `.git/config` of the working directory when it exists, then either the
  file named by `GIT_CONFIG_GLOBAL` alone, or `~/.gitconfig` followed by `$XDG_CONFIG_HOME/git/config`
  (`~/.config/git/config`). Each key is taken from the first file that sets it; within one file the last value wins.
  The system configuration, `GIT_CONFIG_COUNT` and the variables it counts, `[include]` and `[includeIf]` are not
  read.
- **An empty `GIT_CONFIG_GLOBAL`** counts as unset, so `~/.gitconfig` is read. git reads no global file in that case.
- **Keys.** From these files git-gen uses the identity keys, `init.defaultBranch`, `gpg.format`, the signing program
  keys, `user.signingkey` and, from `.git/config`, the URL of the `origin` remote. `commit.gpgSign` is ignored: every
  commit is signed. With `gpg.format = ssh`, `user.signingkey` is required.
- **Signing program keys.** `gpg.program`, `gpg.openpgp.program`, `gpg.ssh.program` and `gpg.x509.program` are read
  from the global files only. In `.git/config` each one is ignored with a warning, where git would use it.
- **Identity.** A missing name or e-mail address ends the run; nothing else is used to fill it in.
- **Linked worktrees and submodules.** A `.git` file ends the run with exit code 1; git follows it to the
  repository.
- **Ownership.** On Unix, `.git` and `.git/config` must be owned by the user running git-gen, or the run ends with
  exit code 1. git makes a similar check that `safe.directory` can lift; git-gen reads no allow list.
- **Hooks and templates.** No git hook runs for the commits, including hooks under `core.hooksPath`. A new `.git` gets
  nothing from `init.templateDir` or from git's default template directory.
- **Ignore rules.** Only the `.gitignore` files of the working tree decide which untracked paths are left out of a
  commit; they are read as git reads them, CRLF line ends and a UTF-8 byte order mark included. The global excludes
  file and `.git/info/exclude` are not read.

## Development

Run the tests:

```sh
go test ./...
```

To measure coverage the way CI does, and write a JUnit report next to it:

```sh
go tool gotestsum --junitfile _test_results/tests.xml -- \
  -race -count=1 -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./...
```

CI uploads both to [Codecov][codecov] from Linux and macOS. `-coverpkg=./...` counts the lines each package's tests
reach in every package, so the end-to-end scripts in the root package add to the coverage of `internal/`.

`go test ./...` also runs the end-to-end scripts under `testdata/script/`. Each script runs git-gen as a separate
process with its own `HOME`, `XDG_CONFIG_HOME` and `GIT_CONFIG_GLOBAL`, a copy of `testdata/gitignore` as the
gitignore checkout, a copy of `testdata/boilerplate` as the boilerplate directory, a signing stub from
`testdata/signer/`, no GitHub token, no `gh` binary, `GOPROXY=off` and its own `GOPATH`. The scripts do not read your
git configuration, key or token and do not use the network.

One test signs with your real git configuration and key. It is skipped unless you set `GIT_GEN_TEST_REAL_SIGNING=1`,
may ask for a passphrase, signs three commits in a temporary directory, and verifies them with the `git` command:

```sh
GIT_GEN_TEST_REAL_SIGNING=1 go test -run TestRealSigning ./internal/repo
```

The license texts under `internal/license/data/` are generated. To regenerate them, run:

```sh
go generate ./internal/license/...
```

It fetches the SPDX License List data at the commit that the tag fixed in `internal/license/spdxgen` names, and
rewrites the `.txt` files, `manifest.json` and `README.md` in that directory. It writes nothing unless every license
passes validation. To add a license, add a row to the table in `internal/license/license.go` and its SPDX ID to the
`//go:generate` line there.

## License data

The license texts embedded in git-gen come from the SPDX License List, version 3.29.0.

- Source: `https://raw.githubusercontent.com/spdx/license-list-data/31ba1a50e5397e00a304dbadc76531740e89ee48/json/`
  (tag `v3.29.0`)
- Copyright: Linux Foundation and its Contributors
- License: [CC-BY-3.0](https://creativecommons.org/licenses/by/3.0/)
- Changes: when git-gen writes a `LICENSE` file, it replaces the line that holds the copyright placeholder with a
  copyright line that names the year and the copyright holder. The embedded texts are not modified.

See `internal/license/data/README.md` and `internal/license/data/manifest.json`.

## License

git-gen is licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE).

<!-- badge links -->
[codecov]: https://app.codecov.io/gh/zchee/git-gen
[codecov-badge]: https://img.shields.io/codecov/c/github/zchee/git-gen/main?logo=codecov&style=for-the-badge
