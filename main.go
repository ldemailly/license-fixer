package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/google/go-github/v72/github"
	"golang.org/x/oauth2"
)

const (
	defaultBranchName = "fix-license-copyright"
	prTitle           = "Fix Apache license copyright placeholder"
	prBody            = "The Apache License template still contains the unfilled copyright\n" +
		"placeholder:\n\n" +
		"```\n" +
		"   Copyright [yyyy] [name of copyright owner]\n" +
		"```\n\n" +
		"This PR replaces it with the correct copyright information.\n"
	// forkWaitDuration is how long to wait for GitHub to finish creating a fork.
	forkWaitDuration = 5 * time.Second
)

// stringSlice is a flag.Value that accumulates repeated -c flags.
type stringSlice []string

func (s *stringSlice) String() string { return strings.Join(*s, ", ") }
func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// githubToken returns the best available GitHub personal access token.
// Priority: -token flag → GH_TOKEN env → GITHUB_TOKEN env → `gh auth token`.
func githubToken(tokenFlag string) string {
	if tokenFlag != "" {
		return tokenFlag
	}
	for _, env := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if t := os.Getenv(env); t != "" {
			return t
		}
	}
	out, err := exec.CommandContext(context.Background(), "gh", "auth", "token").Output()
	if err == nil {
		if t := strings.TrimSpace(string(out)); t != "" {
			return t
		}
	}
	return ""
}

// checker holds the runtime state for a license-checking session.
type checker struct {
	client     *github.Client
	copyrights []string
	fix        bool
	branch     string
	hasErrors  bool
}

// checkRepo fetches the LICENSE file for owner/repo and reports its status.
// If c.fix is true it applies the fix by creating a branch and opening a PR.
func (c *checker) checkRepo(owner, repo string) {
	fmt.Printf("Checking %s/%s\n", owner, repo)

	fileContent, _, resp, err := c.client.Repositories.GetContents(
		context.Background(), owner, repo, "LICENSE", nil)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			fmt.Printf("  No LICENSE file found\n")
			return
		}
		fmt.Fprintf(os.Stderr, "  Error getting LICENSE: %v\n", err)
		c.hasErrors = true
		return
	}

	content, err := fileContent.GetContent()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Error decoding LICENSE: %v\n", err)
		c.hasErrors = true
		return
	}

	if !isLicenseBroken(content) {
		fmt.Printf("  OK: copyright already filled\n")
		return
	}

	fmt.Printf("  NEEDS FIX: copyright template not filled\n")

	if !c.fix {
		return
	}

	if len(c.copyrights) == 0 {
		fmt.Fprintf(os.Stderr, "  Cannot fix: no -c copyright lines provided\n")
		c.hasErrors = true
		return
	}

	if err := c.applyFix(owner, repo, fileContent, content); err != nil {
		fmt.Fprintf(os.Stderr, "  Error applying fix: %v\n", err)
		c.hasErrors = true
	}
}

// applyFix creates a branch, commits the fixed LICENSE, and opens a PR.
func (c *checker) applyFix(owner, repo string, fileContent *github.RepositoryContent, rawContent string) error {
	newContent := fixedLicense(rawContent, c.copyrights)

	// Determine the default branch for the PR base.
	repoInfo, _, err := c.client.Repositories.Get(context.Background(), owner, repo)
	if err != nil {
		return fmt.Errorf("getting repo info: %w", err)
	}
	defaultBranch := repoInfo.GetDefaultBranch()

	// Try to create the fix branch directly on the upstream repo.
	// Fall back to forking if we lack write access.
	fixOwner, fixRepo, err := c.ensureBranch(owner, repo, defaultBranch)
	if err != nil {
		return fmt.Errorf("creating fix branch: %w", err)
	}

	// Resolve the correct file SHA for the repo we are writing to (may be a
	// fork, in which case we need its own SHA for the file).
	sha := fileContent.GetSHA()
	if fixOwner != owner || fixRepo != repo {
		forkFile, _, _, ferr := c.client.Repositories.GetContents(
			context.Background(), fixOwner, fixRepo, "LICENSE", nil)
		if ferr != nil {
			return fmt.Errorf("getting LICENSE from fork: %w", ferr)
		}
		sha = forkFile.GetSHA()
	}

	// Commit the updated file.
	_, _, err = c.client.Repositories.UpdateFile(
		context.Background(), fixOwner, fixRepo, "LICENSE",
		&github.RepositoryContentFileOptions{
			Message: github.Ptr("Fix Apache license copyright template"),
			Content: []byte(newContent),
			SHA:     github.Ptr(sha),
			Branch:  github.Ptr(c.branch),
		})
	if err != nil {
		return fmt.Errorf("updating LICENSE: %w", err)
	}

	// Build the PR head ref, e.g. "mylogin:fix-license-copyright" for forks.
	head := c.branch
	if fixOwner != owner {
		head = fixOwner + ":" + c.branch
	}

	pr, _, err := c.client.PullRequests.Create(context.Background(), owner, repo, &github.NewPullRequest{
		Title: github.Ptr(prTitle),
		Head:  github.Ptr(head),
		Base:  github.Ptr(defaultBranch),
		Body:  github.Ptr(prBody),
	})
	if err != nil {
		return fmt.Errorf("creating PR: %w", err)
	}

	fmt.Printf("  Created PR: %s\n", pr.GetHTMLURL())
	return nil
}

// ensureBranch creates c.branch off baseBranch in owner/repo.
// If the authenticated user has no push access it forks the repo first and
// creates the branch on the fork.
// It returns the owner and repo name where the branch was created.
func (c *checker) ensureBranch(owner, repo, baseBranch string) (string, string, error) {
	// Get the SHA of the tip of the base branch.
	ref, _, err := c.client.Git.GetRef(context.Background(), owner, repo, "refs/heads/"+baseBranch)
	if err != nil {
		return "", "", fmt.Errorf("getting base branch ref: %w", err)
	}
	tipSHA := ref.Object.GetSHA()

	newRef := &github.Reference{
		Ref:    github.Ptr("refs/heads/" + c.branch),
		Object: &github.GitObject{SHA: github.Ptr(tipSHA)},
	}
	_, _, createErr := c.client.Git.CreateRef(context.Background(), owner, repo, newRef)
	if createErr == nil {
		return owner, repo, nil
	}

	// Treat 422 as "already exists" and only fork on permission-related failures.
	var createErrResp *github.ErrorResponse
	if errors.As(createErr, &createErrResp) && createErrResp.Response != nil {
		switch createErrResp.Response.StatusCode {
		case http.StatusUnprocessableEntity:
			return owner, repo, nil
		case http.StatusForbidden, http.StatusNotFound:
			// fall through to fork
		default:
			return "", "", fmt.Errorf("creating branch ref: %w", createErr)
		}
	} else {
		return "", "", fmt.Errorf("creating branch ref: %w", createErr)
	}

	// We likely lack write access. Fork the repo and try again.
	fmt.Printf("  No write access to %s/%s, forking...\n", owner, repo)

	// CreateFork returns the fork data even when err is an *AcceptedError
	// (GitHub responds 202 to indicate the fork is being created).
	fork, _, forkErr := c.client.Repositories.CreateFork(context.Background(), owner, repo, nil)
	if forkErr != nil && !isAcceptedError(forkErr) {
		fork, forkErr = c.resolveForkAlreadyExists(repo, fork, forkErr)
		if forkErr != nil {
			return "", "", fmt.Errorf("fork: original branch error: %w (fork error: %w)", createErr, forkErr)
		}
	}
	if fork == nil {
		if forkErr != nil {
			return "", "", fmt.Errorf("fork returned nil repository: %w", forkErr)
		}
		return "", "", errors.New("fork returned nil repository")
	}

	forkOwner := fork.GetOwner().GetLogin()
	forkRepo := fork.GetName()

	// GitHub may take some time to fully initialize the fork; poll for the base branch ref.
	fmt.Printf("  Waiting for fork %s/%s to be ready...\n", forkOwner, forkRepo)
	var forkRef *github.Reference
	deadline := time.Now().Add(12 * forkWaitDuration) // ~1 minute
	for {
		forkRef, _, err = c.client.Git.GetRef(context.Background(), forkOwner, forkRepo, "refs/heads/"+baseBranch)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			return "", "", fmt.Errorf("getting fork base branch ref (fork may not be ready): %w", err)
		}
		time.Sleep(forkWaitDuration)
	}

	_, _, err = c.client.Git.CreateRef(context.Background(), forkOwner, forkRepo, &github.Reference{
		Ref:    github.Ptr("refs/heads/" + c.branch),
		Object: &github.GitObject{SHA: forkRef.Object.SHA},
	})
	if err != nil && !strings.Contains(err.Error(), "Reference already exists") {
		return "", "", fmt.Errorf("creating branch on fork: %w", err)
	}

	return forkOwner, forkRepo, nil
}

func isAcceptedError(err error) bool {
	var acceptedErr *github.AcceptedError
	return errors.As(err, &acceptedErr)
}

func (c *checker) resolveForkAlreadyExists(
	repo string,
	fork *github.Repository,
	forkErr error,
) (*github.Repository, error) {
	var forkErrResp *github.ErrorResponse
	if !errors.As(forkErr, &forkErrResp) ||
		forkErrResp.Response == nil ||
		forkErrResp.Response.StatusCode != http.StatusUnprocessableEntity {
		return fork, forkErr
	}

	me, _, uerr := c.client.Users.Get(context.Background(), "")
	if uerr != nil {
		return nil, fmt.Errorf("fork already exists but could not determine current user: %w", uerr)
	}
	fork, _, forkErr = c.client.Repositories.Get(context.Background(), me.GetLogin(), repo)
	return fork, forkErr
}

// checkOwner lists all public repos for the given GitHub user or organization
// and calls checkRepo for each one.
func (c *checker) checkOwner(ownerOrUser string) {
	repos, err := c.listPublicRepos(ownerOrUser)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing repos for %s: %v\n", ownerOrUser, err)
		c.hasErrors = true
		return
	}
	fmt.Printf("Found %d public repos for %s\n", len(repos), ownerOrUser)
	for _, r := range repos {
		c.checkRepo(ownerOrUser, r.GetName())
	}
}

// listPublicRepos returns all public repos for ownerOrUser (org or user).
func (c *checker) listPublicRepos(ownerOrUser string) ([]*github.Repository, error) {
	var all []*github.Repository

	// Try as an organization first.
	orgOpts := &github.RepositoryListByOrgOptions{
		Type:        "public",
		ListOptions: github.ListOptions{PerPage: 100},
	}
	for {
		repos, resp, err := c.client.Repositories.ListByOrg(context.Background(), ownerOrUser, orgOpts)
		if err == nil {
			all = append(all, repos...)
			if resp.NextPage == 0 {
				return all, nil
			}
			orgOpts.Page = resp.NextPage
			continue
		}
		// 404 means it is not an org; fall through to user lookup.
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			break
		}
		return nil, fmt.Errorf("listing org repos: %w", err)
	}

	// Fall back to user.
	all = nil
	userOpts := &github.RepositoryListByUserOptions{
		Type:        "public",
		ListOptions: github.ListOptions{PerPage: 100},
	}
	for {
		repos, resp, err := c.client.Repositories.ListByUser(context.Background(), ownerOrUser, userOpts)
		if err != nil {
			return nil, fmt.Errorf("listing user repos: %w", err)
		}
		all = append(all, repos...)
		if resp.NextPage == 0 {
			return all, nil
		}
		userOpts.Page = resp.NextPage
	}
}

func main() {
	var copyrights stringSlice
	var tokenFlag, branchFlag string
	var fix bool

	flag.Var(&copyrights, "c",
		"Copyright line to use for the fix (repeatable).\n\t"+
			`Example: -c "2025 Fortio Authors"`)
	flag.StringVar(&tokenFlag, "token", "",
		"GitHub personal access token.\n\t"+
			"Defaults to GH_TOKEN / GITHUB_TOKEN env vars, or 'gh auth token'.")
	flag.BoolVar(&fix, "fix", false,
		"Apply fixes by creating a branch and opening a PR.")
	flag.StringVar(&branchFlag, "branch", defaultBranchName,
		"Branch name to use when creating the fix.")

	flag.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: license-fixer [flags] <owner/repo|owner|org>\n"+
			"\n"+
			"Check whether the Apache LICENSE file in one or more GitHub repositories still\n"+
			"contains the unfilled copyright placeholder:\n"+
			"\n"+
			"   Copyright [yyyy] [name of copyright owner]\n"+
			"\n"+
			"With -fix the tool creates a branch, commits the corrected LICENSE, and opens\n"+
			"a pull request.  Provide the actual copyright text with -c (repeatable):\n"+
			"\n"+
			`   license-fixer -fix -c "2025 Fortio Authors" fortio/template`+"\n"+
			"   license-fixer -fix \\\n"+
			`       -c "2016 Fortio Authors" \`+"\n"+
			`       -c "2015 Michal Witkowski (dflag/)" \`+"\n"+
			"       fortio\n"+
			"\n"+
			"Authentication\n"+
			"--------------\n"+
			"The tool uses a GitHub personal access token.  Resolution order:\n"+
			"\n"+
			"  1. -token flag\n"+
			"  2. GH_TOKEN environment variable\n"+
			"  3. GITHUB_TOKEN environment variable\n"+
			"  4. Output of: gh auth token\n"+
			"\n"+
			"For read-only checking a token is not strictly required (though you will hit\n"+
			"the unauthenticated rate limit of 60 req/h quickly).\n"+
			"\n"+
			"For -fix the token needs:\n"+
			"  * repo scope - to read/write files and open PRs on repos you own or have\n"+
			"    write access to.\n"+
			"  * public_repo scope (subset of repo) - sufficient for public repos.\n"+
			"\n"+
			"If the token does not have write access to a repository the tool will\n"+
			"automatically fork it to your account and open a cross-repository PR.\n"+
			"\n"+
			"To obtain a token:\n"+
			"  * Run: gh auth login  (then 'gh auth token' returns a usable token)\n"+
			"  * Or create a fine-grained PAT at https://github.com/settings/tokens\n"+
			"\n"+
			"Flags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	args := flag.Args()
	if len(args) != 1 {
		flag.Usage()
		os.Exit(1)
	}

	token := githubToken(tokenFlag)

	var client *github.Client

	if token != "" {
		ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
		tc := oauth2.NewClient(context.Background(), ts)
		client = github.NewClient(tc)
	} else {
		client = github.NewClient(nil)
		fmt.Fprintln(os.Stderr, "Warning: no GitHub token found; unauthenticated rate limits apply (60 req/h).")
	}

	ch := &checker{
		client:     client,
		copyrights: copyrights,
		fix:        fix,
		branch:     branchFlag,
	}

	target := args[0]
	if strings.Contains(target, "/") {
		parts := strings.SplitN(target, "/", 2)
		ch.checkRepo(parts[0], parts[1])
	} else {
		ch.checkOwner(target)
	}

	if ch.hasErrors {
		os.Exit(1)
	}
}
