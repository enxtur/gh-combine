package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/cli/go-gh"
)

type Commit struct {
	Oid string `json:"oid"`
}
type PRInfo struct {
	Number      int      `json:"number"`
	HeadRefName string   `json:"headRefName"`
	Title       string   `json:"title"`
	Body        string   `json:"body"`
	Commits     []Commit `json:"commits"`
}

func git(args ...string) error {
	cmd := exec.Command("git", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// gitQuiet runs git and ignores failure, for commands that are expected to fail
// in the normal case (nothing to abort, no branch to delete).
func gitQuiet(args ...string) {
	_ = exec.Command("git", args...).Run()
}

// headBranchName is the branch the combined commits are built on, derived from
// the arguments the user passed so that re-running the same command reuses it.
func headBranchName(userArgs []string) string {
	return fmt.Sprintf("combine-prs-%s", strings.Join(userArgs, "-"))
}

// cherryPickArgs is the git invocation that replays a PR's commits, or nil for
// a PR with no commits to replay.
func cherryPickArgs(prInfo PRInfo) []string {
	if len(prInfo.Commits) == 0 {
		return nil
	}
	args := []string{"cherry-pick"}
	for _, commit := range prInfo.Commits {
		args = append(args, commit.Oid)
	}
	return args
}

func prTitle(prInfoList []PRInfo) string {
	prNumbers := []string{}
	for _, prInfo := range prInfoList {
		prNumbers = append(prNumbers, fmt.Sprintf("#%d", prInfo.Number))
	}
	return fmt.Sprintf("Combine %s", strings.Join(prNumbers, ", "))
}

func prBody(prInfoList []PRInfo) string {
	var body strings.Builder
	body.WriteString("Combines the following pull requests:\n\n")
	for _, prInfo := range prInfoList {
		fmt.Fprintf(&body, "- #%d %s\n", prInfo.Number, prInfo.Title)
	}
	return body.String()
}

func viewPR(arg string) (PRInfo, error) {
	stdout, stderr, err := gh.Exec("pr", "view", arg, "--json", "number,headRefName,title,body,commits")
	if err != nil {
		return PRInfo{}, fmt.Errorf("gh pr view %s: %w: %s", arg, err, strings.TrimSpace(stderr.String()))
	}
	var prInfo PRInfo
	if err := json.NewDecoder(&stdout).Decode(&prInfo); err != nil {
		return PRInfo{}, fmt.Errorf("gh pr view %s: %w", arg, err)
	}
	return prInfo, nil
}

func main() {
	userArgs := os.Args[1:]
	if len(userArgs) == 0 {
		log.Fatal("usage: gh-combine <pr> [<pr>...]")
	}

	const baseBranchName = "main"

	prInfoList := []PRInfo{}

	for _, arg := range userArgs {
		prInfo, err := viewPR(arg)
		if err != nil {
			log.Fatal(err)
		}
		prInfoList = append(prInfoList, prInfo)
	}

	headBranchName := headBranchName(userArgs)

	// A previous run may have died mid-cherry-pick, which blocks switching branches.
	gitQuiet("cherry-pick", "--abort")

	if err := git("switch", baseBranchName); err != nil {
		log.Fatal(err)
	}

	// The branch only exists if a previous run created it.
	gitQuiet("branch", "-D", headBranchName)

	if err := git("switch", "-c", headBranchName); err != nil {
		log.Fatal(err)
	}

	for _, prInfo := range prInfoList {
		args := cherryPickArgs(prInfo)
		if args == nil {
			continue
		}

		if err := git(args...); err != nil {
			// Leave the repo out of the cherry-pick so the next run can switch branches.
			gitQuiet("cherry-pick", "--abort")
			log.Fatal(err)
		}
	}

	if err := git("push", "--force", "--set-upstream", "origin", headBranchName); err != nil {
		log.Fatal(err)
	}

	stdout, stderr, err := gh.Exec(
		"pr", "create",
		"--base", baseBranchName,
		"--head", headBranchName,
		"--title", prTitle(prInfoList),
		"--body", prBody(prInfoList),
	)
	if err != nil {
		log.Fatalf("gh pr create: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	println("Created PR:", strings.TrimSpace(stdout.String()))
}
