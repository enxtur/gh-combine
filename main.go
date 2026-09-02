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

func main() {
	userArgs := os.Args[1:]
	if len(userArgs) == 0 {
		log.Fatal("usage: gh-combine <pr> [<pr>...]")
	}

	const baseBranchName = "main"

	prInfoList := []PRInfo{}

	for _, arg := range userArgs {
		prInfoBuffer, stderr, err := gh.Exec("pr", "view", arg, "--json", "number,headRefName,title,body,commits")
		if err != nil {
			log.Fatalf("gh pr view %s: %v: %s", arg, err, strings.TrimSpace(stderr.String()))
		}
		var prInfo PRInfo
		err = json.NewDecoder(&prInfoBuffer).Decode(&prInfo)
		if err != nil {
			log.Fatal(err)
		}
		prInfoList = append(prInfoList, prInfo)
	}

	headBranchName := fmt.Sprintf("combine-prs-%s", strings.Join(userArgs, "-"))

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
		if len(prInfo.Commits) == 0 {
			continue
		}

		cherryPickArgs := []string{"cherry-pick"}
		for _, commit := range prInfo.Commits {
			cherryPickArgs = append(cherryPickArgs, commit.Oid)
		}

		if err := git(cherryPickArgs...); err != nil {
			// Leave the repo out of the cherry-pick so the next run can switch branches.
			gitQuiet("cherry-pick", "--abort")
			log.Fatal(err)
		}
	}

	if err := git("push", "--force", "--set-upstream", "origin", headBranchName); err != nil {
		log.Fatal(err)
	}

	prNumbers := []string{}
	var prBody strings.Builder
	prBody.WriteString("Combines the following pull requests:\n\n")
	for _, prInfo := range prInfoList {
		prNumbers = append(prNumbers, fmt.Sprintf("#%d", prInfo.Number))
		fmt.Fprintf(&prBody, "- #%d %s\n", prInfo.Number, prInfo.Title)
	}
	prTitle := fmt.Sprintf("Combine %s", strings.Join(prNumbers, ", "))

	stdout, stderr, err := gh.Exec(
		"pr", "create",
		"--base", baseBranchName,
		"--head", headBranchName,
		"--title", prTitle,
		"--body", prBody.String(),
	)
	if err != nil {
		log.Fatalf("gh pr create: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	println("Created PR:", strings.TrimSpace(stdout.String()))
}
