package filescom

import (
	"strings"
	"testing"
)

const (
	commandsWorkflow   = "pull-request-commands.yml"
	acceptanceWorkflow = "acceptance-tests.yml"
	acceptanceCommand  = "/run-acceptance-tests"
	liveTestsLabel     = "run-live-tests"
	// The jobs that read the API key, plus the sentinel that only waits for the others.
	gatedJob     = "test_examples"
	sentinelJob  = "sentinel"
	jobCondition = "\n    if:"
)

// between answers the text a pair of markers encloses, so a test can read a literal out of the
// workflow that acts on it rather than out of a copy somebody typed twice.
func between(t *testing.T, body, opening, closing, what string) string {
	t.Helper()
	start := strings.Index(body, opening)
	if start < 0 {
		t.Fatalf("no %s: nothing carries %q", what, opening)
	}
	rest := body[start+len(opening):]
	end := strings.Index(rest, closing)
	if end < 0 {
		t.Fatalf("no %s: %q is never closed by %q", what, opening, closing)
	}
	return rest[:end]
}

// The note workflow runs on the base branch with this repository's token and its API key. A
// checkout there would run a contributor's code with both, so it checks nothing out.
func TestTheNoteWorkflowRunsNoCodeFromTheBranch(t *testing.T) {
	body := readWorkflow(t, commandsWorkflow)
	if !strings.Contains(body, "pull_request_target:") {
		t.Fatal("the note workflow must run on pull_request_target to comment on a fork pull request")
	}
	if strings.Contains(body, "uses:") {
		t.Error("the note workflow runs an action; it must run no step that reads the branch")
	}
	if strings.Contains(body, "pull-requests: write") == false {
		t.Error("the note workflow cannot comment without write access to the pull request")
	}
}

// Anyone can comment on a pull request, so the command flow reads who commented before it reads
// anything else. These three associations are the ones GitHub reports for write access.
func TestTheAcceptanceCommandChecksWhoCommented(t *testing.T) {
	condition := between(t, readWorkflow(t, acceptanceWorkflow), "    if: >-", "\n    permissions:",
		"authorize condition")

	for _, required := range []string{
		"github.event.issue.pull_request",
		"github.event.comment.author_association",
		`"OWNER"`,
		`"MEMBER"`,
		`"COLLABORATOR"`,
		acceptanceCommand,
	} {
		if !strings.Contains(condition, required) {
			t.Errorf("the authorize condition never reads %s:\n%s", required, condition)
		}
	}
	if strings.Contains(condition, "CONTRIBUTOR\"") {
		t.Error("CONTRIBUTOR is any account that landed a commit here, not a maintainer")
	}
}

// The maintainer reads the changes, then types the command. A checkout by branch name would run
// whatever landed after that, so the run reads the commit the authorize job recorded.
func TestTheAcceptanceRunReadsOnlyThePinnedCommit(t *testing.T) {
	jobs := jobBlocks(readWorkflow(t, acceptanceWorkflow))

	pin := jobs["authorize"]
	if !strings.Contains(pin, "gh api \"repos/$REPOSITORY/pulls/$PR_NUMBER\" --jq .head.sha") {
		t.Error("the authorize job does not read the head commit of the pull request")
	}
	if !strings.Contains(pin, "head_sha=$head_sha") {
		t.Error("the authorize job never hands the commit on")
	}
	// An empty answer written to the output leaves the checkout below with no ref, and a
	// checkout with no ref reads the default branch.
	if !strings.Contains(pin, `if [ -z "$head_sha" ]; then`) {
		t.Error("the authorize job hands on a commit it never checked for emptiness")
	}

	run := jobs["acceptance_tests"]
	// This link is the whole gate: the condition that reads the commenter sits on the authorize
	// job, so an acceptance job that does not wait for it runs for every comment.
	if !strings.Contains(run, "needs: authorize") {
		t.Error("the acceptance job does not wait for the authorize job, so nothing checks who asked")
	}
	if !strings.Contains(run, "ref: ${{ needs.authorize.outputs.head_sha }}") {
		t.Error("the acceptance job does not check out the commit the authorize job pinned")
	}
	for _, moving := range []string{"github.head_ref", "github.event.issue.number }}\n          ref"} {
		if strings.Contains(run, moving) {
			t.Errorf("the acceptance job reads %s, which names a branch rather than a commit", moving)
		}
	}
}

// A maintainer asked for this run, so the key is meant to be there. A suite that skipped itself
// for a missing key would report a pass nobody earned.
func TestTheAcceptanceRunRefusesAMissingAPIKey(t *testing.T) {
	run := jobBlocks(readWorkflow(t, acceptanceWorkflow))["acceptance_tests"]
	if !strings.Contains(run, credentialSecret+": ${{ secrets."+credentialSecret+" }}") {
		t.Fatalf("the acceptance job never reads %s", credentialSecret)
	}
	refusal := between(t, run, "- name: Refuse a missing API key", "- name: Check out", "refusal step")
	if !strings.Contains(refusal, "exit 1") {
		t.Errorf("the acceptance job reports a missing key without failing:\n%s", refusal)
	}
}

// The command flow checks a contributor's commit out and runs it. A token that could write to
// this repository would travel with that code.
func TestTheCommandFlowGrantsNoWriteAccess(t *testing.T) {
	body := readWorkflow(t, acceptanceWorkflow)

	granted := between(t, body, "\npermissions:", "\nenv:", "workflow permissions")
	if !strings.Contains(granted, "contents: read") {
		t.Errorf("the command flow does not hold the repository at read:%s", granted)
	}
	if strings.Contains(granted, "write") {
		t.Errorf("the command flow grants write access to the code it runs:%s", granted)
	}
	if strings.Contains(jobBlocks(body)["acceptance_tests"], "permissions:") {
		t.Error("the job that runs the contributor's code widens its own permissions")
	}
}

// The credentialed job spends the vendor account, so a pull request reaches it only from this
// repository and only when a maintainer adds the label.
func TestTheLiveJobsNeedTheLabelOnAPullRequest(t *testing.T) {
	body := readWorkflow(t, "pull-request.yml")
	if !strings.Contains(between(t, body, "on:", "jobs:", "trigger block"), "- labeled") {
		t.Error("pull-request.yml does not run on a label, so adding one starts nothing")
	}

	condition := between(t, jobBlocks(body)[gatedJob], "if: >-", "steps:", "credential gate")
	for _, required := range []string{
		"github.event.pull_request.head.repo.full_name == github.repository",
		"github.event.pull_request.labels.*.name",
		liveTestsLabel,
	} {
		if !strings.Contains(condition, required) {
			t.Errorf("the %s gate never reads %s:\n%s", gatedJob, required, condition)
		}
	}
}

// Every check that spends nothing runs on every pull request, fork or not. A condition on one of
// them would report a skip, and a skipped required job reads as a pass.
func TestTheKeylessJobsRunOnEveryPullRequest(t *testing.T) {
	for _, workflow := range buildWorkflows() {
		t.Run(workflow, func(t *testing.T) {
			read := 0
			for name, block := range jobBlocks(readWorkflow(t, workflow)) {
				if name == gatedJob || name == sentinelJob {
					continue
				}
				read++
				if strings.Contains(block, jobCondition) {
					t.Errorf("job %s carries a condition, so it can skip on a pull request", name)
				}
			}
			if read == 0 {
				t.Fatal("this workflow declares no keyless job, so the scan read nothing")
			}
		})
	}
}

// The note is the only place a maintainer reads the command from. A rename in the workflow that
// never reached the note would print an instruction that starts nothing.
func TestTheNoteNamesTheCommandAndTheLabelTheWorkflowsRead(t *testing.T) {
	command := between(t, readWorkflow(t, acceptanceWorkflow),
		"startsWith(github.event.comment.body, '", "'", "command the acceptance workflow matches")
	if command != acceptanceCommand {
		t.Errorf("the acceptance workflow answers %q and this repository decided on %q",
			command, acceptanceCommand)
	}

	label := between(t, readWorkflow(t, "pull-request.yml"),
		"contains(github.event.pull_request.labels.*.name, '", "'", "label the credential gate reads")
	if label != liveTestsLabel {
		t.Errorf("the credential gate reads the label %q and this repository decided on %q",
			label, liveTestsLabel)
	}

	note := readWorkflow(t, commandsWorkflow)
	for _, named := range []string{"`" + command + "`", "`" + label + "`"} {
		if !strings.Contains(note, named) {
			t.Errorf("the note never names %s, so a maintainer cannot find it", named)
		}
	}
}
