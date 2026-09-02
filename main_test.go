package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHeadBranchName(t *testing.T) {
	tests := []struct {
		name     string
		userArgs []string
		want     string
	}{
		{"single PR", []string{"12"}, "combine-prs-12"},
		{"several PRs", []string{"12", "34", "56"}, "combine-prs-12-34-56"},
		{"order is preserved", []string{"56", "12"}, "combine-prs-56-12"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := headBranchName(test.userArgs); got != test.want {
				t.Errorf("headBranchName(%q) = %q, want %q", test.userArgs, got, test.want)
			}
		})
	}
}

func TestCherryPickArgs(t *testing.T) {
	t.Run("one arg per commit, in order", func(t *testing.T) {
		prInfo := PRInfo{Commits: []Commit{{Oid: "aaa"}, {Oid: "bbb"}}}
		got := cherryPickArgs(prInfo)
		want := []string{"cherry-pick", "aaa", "bbb"}
		if len(got) != len(want) {
			t.Fatalf("cherryPickArgs() = %q, want %q", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("cherryPickArgs() = %q, want %q", got, want)
			}
		}
	})

	// A PR with no commits must be skipped rather than producing a bare
	// "git cherry-pick", which would fail the whole run.
	t.Run("no commits yields no invocation", func(t *testing.T) {
		if got := cherryPickArgs(PRInfo{}); got != nil {
			t.Errorf("cherryPickArgs(PRInfo{}) = %q, want nil", got)
		}
	})
}

func TestPRTitle(t *testing.T) {
	prInfoList := []PRInfo{{Number: 12}, {Number: 34}, {Number: 56}}
	want := "Combine #12, #34, #56"
	if got := prTitle(prInfoList); got != want {
		t.Errorf("prTitle() = %q, want %q", got, want)
	}
}

func TestPRBody(t *testing.T) {
	prInfoList := []PRInfo{
		{Number: 12, Title: "Bump foo"},
		{Number: 34, Title: "Bump bar"},
	}
	want := "Combines the following pull requests:\n\n- #12 Bump foo\n- #34 Bump bar\n"
	if got := prBody(prInfoList); got != want {
		t.Errorf("prBody() = %q, want %q", got, want)
	}
}

// The JSON shape is dictated by `gh pr view --json`; this pins the field names
// the struct tags depend on.
func TestPRInfoDecoding(t *testing.T) {
	const ghOutput = `{
		"number": 12,
		"headRefName": "dependabot/go_modules/foo-1.2.3",
		"title": "Bump foo from 1.2.2 to 1.2.3",
		"body": "release notes",
		"commits": [{"oid": "aaa"}, {"oid": "bbb"}]
	}`

	var prInfo PRInfo
	if err := json.Unmarshal([]byte(ghOutput), &prInfo); err != nil {
		t.Fatalf("decoding gh output: %v", err)
	}

	if prInfo.Number != 12 {
		t.Errorf("Number = %d, want 12", prInfo.Number)
	}
	if prInfo.HeadRefName != "dependabot/go_modules/foo-1.2.3" {
		t.Errorf("HeadRefName = %q", prInfo.HeadRefName)
	}
	if prInfo.Title != "Bump foo from 1.2.2 to 1.2.3" {
		t.Errorf("Title = %q", prInfo.Title)
	}
	if prInfo.Body != "release notes" {
		t.Errorf("Body = %q", prInfo.Body)
	}
	if len(prInfo.Commits) != 2 || prInfo.Commits[0].Oid != "aaa" || prInfo.Commits[1].Oid != "bbb" {
		t.Errorf("Commits = %+v", prInfo.Commits)
	}
}

// testRepo creates a git repo with one commit on `main` and chdirs into it for
// the duration of the test, since git() runs against the working directory.
func testRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}

	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial commit")

	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatal(err)
		}
	})

	return dir
}

func TestGitSuccess(t *testing.T) {
	testRepo(t)

	if err := git("switch", "-c", "combine-prs-12"); err != nil {
		t.Fatalf("git switch: %v", err)
	}

	out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "combine-prs-12" {
		t.Errorf("current branch = %q, want %q", got, "combine-prs-12")
	}
}

func TestGitFailureIncludesCommandAndStderr(t *testing.T) {
	testRepo(t)

	err := git("switch", "no-such-branch")
	if err == nil {
		t.Fatal("git switch to a missing branch: got nil error, want failure")
	}

	message := err.Error()
	if !strings.Contains(message, "git switch no-such-branch") {
		t.Errorf("error %q does not name the failing command", message)
	}
	if !strings.Contains(message, "exit status") {
		t.Errorf("error %q does not carry git's exit status", message)
	}
	// Without git's own stderr the user cannot tell why the run stopped.
	if !strings.Contains(message, "fatal:") {
		t.Errorf("error %q does not carry git's stderr", message)
	}
}

// gitQuiet exists so the recovery steps of a rerun (aborting a stale
// cherry-pick, deleting the previous branch) do not abort the run when there is
// nothing to recover.
func TestGitQuietIgnoresFailure(t *testing.T) {
	testRepo(t)

	gitQuiet("cherry-pick", "--abort")
	gitQuiet("branch", "-D", "combine-prs-12")

	if err := git("switch", "-c", "combine-prs-12"); err != nil {
		t.Fatalf("git switch after no-op recovery: %v", err)
	}
}

// The whole point of the tool: commits from each PR land on the new branch in
// argument order.
func TestCherryPickReplaysCommitsInOrder(t *testing.T) {
	dir := testRepo(t)

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %s: %v", strings.Join(args, " "), err)
		}
		return strings.TrimSpace(string(out))
	}
	commitFile := func(name, message string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", name)
		run("commit", "-m", message)
		return run("rev-parse", "HEAD")
	}

	// Two PR branches, each off main, each with one commit.
	run("switch", "-c", "pr-12")
	oid12 := commitFile("a.txt", "add a")
	run("switch", "main")
	run("switch", "-c", "pr-34")
	oid34 := commitFile("b.txt", "add b")
	run("switch", "main")

	prInfoList := []PRInfo{
		{Number: 12, Commits: []Commit{{Oid: oid12}}},
		{Number: 34, Commits: []Commit{{Oid: oid34}}},
	}

	if err := git("switch", "-c", headBranchName([]string{"12", "34"})); err != nil {
		t.Fatal(err)
	}
	for _, prInfo := range prInfoList {
		args := cherryPickArgs(prInfo)
		if args == nil {
			continue
		}
		if err := git(args...); err != nil {
			t.Fatalf("cherry-pick: %v", err)
		}
	}

	got := strings.Split(run("log", "--format=%s", "main..HEAD", "--reverse"), "\n")
	want := []string{"add a", "add b"}
	if len(got) != len(want) {
		t.Fatalf("commits on combined branch = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("commits on combined branch = %q, want %q", got, want)
		}
	}

	for _, name := range []string{"a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s missing from combined branch: %v", name, err)
		}
	}
}

// A conflicting cherry-pick must leave the repo out of the conflicted state, or
// the next run cannot even switch branches.
func TestCherryPickConflictIsAbortedAndReported(t *testing.T) {
	dir := testRepo(t)

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %s: %v", strings.Join(args, " "), err)
		}
		return strings.TrimSpace(string(out))
	}
	commitFile := func(name, contents, message string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", name)
		run("commit", "-m", message)
		return run("rev-parse", "HEAD")
	}

	// Two branches touching the same file with different contents.
	run("switch", "-c", "pr-12")
	commitFile("conflict.txt", "from twelve\n", "add conflict from 12")
	run("switch", "main")
	run("switch", "-c", "pr-34")
	oid34 := commitFile("conflict.txt", "from thirty-four\n", "add conflict from 34")
	run("switch", "pr-12")

	err := git(cherryPickArgs(PRInfo{Number: 34, Commits: []Commit{{Oid: oid34}}})...)
	if err == nil {
		t.Fatal("conflicting cherry-pick: got nil error, want failure")
	}
	if !strings.Contains(err.Error(), "cherry-pick") {
		t.Errorf("error %q does not name the failing command", err)
	}

	gitQuiet("cherry-pick", "--abort")

	// After the abort the repo is usable again.
	if _, statErr := os.Stat(filepath.Join(dir, ".git", "CHERRY_PICK_HEAD")); !os.IsNotExist(statErr) {
		t.Errorf("CHERRY_PICK_HEAD still present after abort: %v", statErr)
	}
	if err := git("switch", "main"); err != nil {
		t.Errorf("switching branches after abort: %v", err)
	}
}
