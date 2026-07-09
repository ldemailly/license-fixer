# license-fixer

A Go CLI that checks Apache `LICENSE` files in GitHub repositories and
optionally fixes the unfilled copyright placeholder:

```
   Copyright [yyyy] [name of copyright owner]
```

## Installation

```sh
go install github.com/ldemailly/license-fixer@latest
```

## Usage

```
license-fixer [flags] <owner/repo|owner|org>
```

Point it at a single repository, a GitHub user, or an organisation.
When given a user or org it checks **all public repositories**.

### Check only (read-only)

```sh
# single repo
license-fixer fortio/template

# all public repos for an org
license-fixer fortio

# all public repos for a user
license-fixer ldemailly
```

### Fix (creates a branch + PR)

Provide the replacement copyright text with `-c` (repeatable for multiple lines):

```sh
# single copyright line
license-fixer -fix -c "2025 Fortio Authors" fortio/template

# multiple copyright lines (like fortio/fortio)
license-fixer -fix \
    -c "2016 Fortio Authors" \
    -c "2015 Michal Witkowski (dflag/)" \
    fortio/fortio

# fix every unfilled repo under an org
license-fixer -fix -c "2025 Fortio Authors" fortio
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-c` | — | Copyright line(s) for the fix (repeatable) |
| `-fix` | false | Create a branch and open a PR |
| `-branch` | `fix-license-copyright` | Branch name used for the fix |
| `-token` | — | GitHub personal access token (see below) |

## Authentication & permissions

The tool uses a **GitHub personal access token** resolved in this order:

1. `-token` flag
2. `GH_TOKEN` environment variable
3. `GITHUB_TOKEN` environment variable
4. Output of `gh auth token` (if the [GitHub CLI](https://cli.github.com/) is installed)

For **read-only checking** a token is optional, but unauthenticated requests are
limited to **60 requests/hour**.

For **`-fix`** the token needs:

* **`repo`** scope – to read/write files and open PRs on repositories you own
  or have explicit write access to.
* **`public_repo`** (a subset of `repo`) – sufficient for public repos you want
  to fix directly.

### How to obtain a token

**Option A – GitHub CLI (recommended)**

```sh
# authenticate once
gh auth login

# the token is then available automatically to license-fixer, or explicitly:
license-fixer -token "$(gh auth token)" fortio/template
```

**Option B – Personal Access Token**

1. Go to <https://github.com/settings/tokens/new>
2. Select scopes: `public_repo` (or `repo` for private repos)
3. Copy the generated token and export it:

```sh
export GH_TOKEN=ghp_...
license-fixer -fix -c "2025 Fortio Authors" fortio/template
```

### Repositories you don't own

If the token lacks **write access** to a repository (e.g. you are fixing
someone else's repo), the tool will automatically:

1. **Fork** the repository to your account
2. Create the fix branch on the fork
3. Commit the corrected `LICENSE`
4. Open a **cross-repository pull request** from `yourlogin:fix-license-copyright`
   to the upstream default branch
