package filescom

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const proseScript = "scripts/lint-prose.sh"

// standardNeedle is assembled at run time. The check greps every tracked file for the writing
// standard's name, so this file must not carry it as one literal.
func standardNeedle() string { return "S" + "TE" + "-100" }

// runProseCheck writes body to a file and runs the check over that file alone.
func runProseCheck(t *testing.T, body string) (string, error) {
	t.Helper()
	page := filepath.Join(t.TempDir(), "page.md")
	if err := os.WriteFile(page, []byte(body), 0o600); err != nil {
		t.Fatalf("write the fixture page: %v", err)
	}
	cmd := exec.Command("./"+proseScript, page)
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestTheProseCheckPassesOnTextThatFollowsTheRules is the control. Without it every test below
// would pass on a check that rejects everything.
func TestTheProseCheckPassesOnTextThatFollowsTheRules(t *testing.T) {
	out, err := runProseCheck(t, "# Groups\n\nA group holds users.\n\nThe provider creates it.\n")
	if err != nil {
		t.Fatalf("the check rejected text that follows the rules: %v\n%s", err, out)
	}
}

// TestTheProseCheckNamesTheLineOfABannedWord reads the report, not only the exit code. A finding
// nobody can locate costs more to act on than it saves.
func TestTheProseCheckNamesTheLineOfABannedWord(t *testing.T) {
	out, err := runProseCheck(t, "# Groups\n\nA group holds users.\n\nThe provider will utilize it.\n")
	if err == nil {
		t.Fatalf("the check passed on a banned word:\n%s", out)
	}
	if !strings.Contains(out, ":5:") {
		t.Errorf("the check does not name line 5, where the banned word is:\n%s", out)
	}
	if !strings.Contains(out, "utilize") {
		t.Errorf("the check does not name the banned word:\n%s", out)
	}
}

// TestTheProseCheckNamesTheLineOfALongSentence covers the second rule. The limit is a count, so
// the fixture is built from it rather than from a sentence somebody counted by hand.
func TestTheProseCheckNamesTheLineOfALongSentence(t *testing.T) {
	var words []string
	for index := range 30 {
		words = append(words, fmt.Sprintf("word%d", index))
	}
	out, err := runProseCheck(t, "# Groups\n\n"+strings.Join(words, " ")+".\n")
	if err == nil {
		t.Fatalf("the check passed on a sentence of %d words:\n%s", len(words), out)
	}
	if !strings.Contains(out, ":3:") {
		t.Errorf("the check does not name line 3, where the sentence starts:\n%s", out)
	}
	if !strings.Contains(out, "30 words") {
		t.Errorf("the check does not report the length it counted:\n%s", out)
	}
}

// TestTheProseCheckNamesTheLineThatCitesTheWritingStandard covers the rule that keeps the
// standard's own name out of everything this repository ships.
func TestTheProseCheckNamesTheLineThatCitesTheWritingStandard(t *testing.T) {
	out, err := runProseCheck(t, "# Groups\n\nThis page follows "+standardNeedle()+".\n")
	if err == nil {
		t.Fatalf("the check passed on a page naming the writing standard:\n%s", out)
	}
	if !strings.Contains(out, ":3:") {
		t.Errorf("the check does not name line 3, where the reference is:\n%s", out)
	}
}

// TestTheProseCheckReadsNoCodeBlockOrSpan keeps the rules off text the reader is meant to type.
// A banned word inside a code sample is the API's word, not ours.
func TestTheProseCheckReadsNoCodeBlockOrSpan(t *testing.T) {
	body := "# Groups\n\nSet the `utilize` property.\n\n```go\nutilize := true\n```\n"
	out, err := runProseCheck(t, body)
	if err != nil {
		t.Fatalf("the check read a banned word out of a code sample: %v\n%s", err, out)
	}
}
