// Copyright 2026, Pulumi Corporation.  All rights reserved.

package examples

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// This file carries no build tag on purpose. It reads files and starts nothing, so the scan
// runs on a plain `go test ./...` and a person sees a leaked credential before they commit it.

// The cassettes of this package live under one directory and carry one suffix. Both names sit
// in this untagged file, so the scan and the recorder read the same spelling under every tag.
const (
	cassetteDirName = "testdata/cassettes"
	cassetteSuffix  = ".yaml"
)

// The header names a cassette must never carry. They sit in this untagged file so the scan and
// the save hook read one spelling. A cassette holds the Go canonical form of each name.
//
//nolint:gosec // G101: each of these names a header of a request, and holds no key.
const (
	headerFilesAPIKey   = "X-Filesapi-Key"
	headerAPIKey        = "X-Api-Key"
	headerAuthorization = "Authorization"
	headerCookie        = "Cookie"
	headerSetCookie     = "Set-Cookie"
)

// credentialHeaders is the whole set. The save hook drops each one, so a name in a committed
// file means the hook did not run or somebody wrote the file by hand.
var credentialHeaders = []string{
	headerFilesAPIKey, headerAPIKey, headerAuthorization, headerCookie, headerSetCookie,
}

// redactionMarker is what the save hook writes in place of a secret. It sits in this untagged
// file, so the scrub and the scan read one spelling under every tag.
const redactionMarker = "REDACTED"

// secretMemberNames are the JSON member names a body carries a secret under. The save hook
// replaces the string value of each one on every path, and the scan below reads the same list.
// The longer names come first, so a match reads `aws_secret_key` rather than the `key` inside it.
//
//nolint:gosec // G101: each of these names a JSON member, and holds no key.
var secretMemberNames = []string{
	"aws_secret_key", "awsSecretKey", "basic_auth", "basicAuth", "authorization",
	"api_key", "apiKey", "password", "secret", "token", "key",
}

// secretMember matches one member name and the value that follows it, in a JSON body or in a
// YAML line. The name sits between word boundaries, so `token_type` is not a token.
var secretMember = regexp.MustCompile(
	`(?i)\b(` + strings.Join(secretMemberNames, "|") + `)\b["']?\s*:\s*["']?([^"',}\]\s]*)`)

// longToken matches the shape of a Files.com key: a long run of key characters. The scan looks
// for one on a line that also names a credential header, which is where a leak lands.
var longToken = regexp.MustCompile(`[A-Za-z0-9_-]{20,}`)

// cassetteFindings returns one line for every credential the file content carries.
func cassetteFindings(name, content string) []string {
	var findings []string
	for index, line := range strings.Split(content, "\n") {
		lower := strings.ToLower(line)
		named := false
		for _, header := range credentialHeaders {
			if !strings.Contains(lower, strings.ToLower(header)+":") {
				continue
			}
			named = true
			findings = append(findings,
				fmt.Sprintf("%s line %d names the %s header", name, index+1, header))
			if longToken.MatchString(strings.SplitN(line, ":", 2)[1]) {
				findings = append(findings,
					fmt.Sprintf("%s line %d holds a token after the %s header", name, index+1, header))
			}
		}
		// A line the header scan reported already names the credential it carries, so the
		// member scan below would report the same leak a second time.
		if named {
			continue
		}
		findings = append(findings, memberFindings(name, index+1, line)...)
	}
	return findings
}

// memberFindings reports every secret member on one line that still carries a value. An empty
// value is a YAML member whose value sits on the next line, and the header scan covers those.
// A value that opens a list carries its secrets in the elements, so the list scan reads those.
func memberFindings(name string, number int, line string) []string {
	var findings []string
	for _, match := range secretMember.FindAllStringSubmatchIndex(line, -1) {
		member, value := line[match[2]:match[3]], line[match[4]:match[5]]
		if strings.HasPrefix(value, "[") {
			findings = append(findings, listFindings(name, number, member, line[match[5]:])...)
			continue
		}
		// A member the vendor answers with no value holds nothing to leak, and the save hook leaves
		// a null alone so the body keeps its shape.
		if value == "" || value == "null" || value == redactionMarker {
			continue
		}
		findings = append(findings,
			fmt.Sprintf("%s line %d holds a value under the %s member", name, number, member))
	}
	return findings
}

// listElement matches one quoted element of a list.
var listElement = regexp.MustCompile(`"([^"]*)"`)

// listFindings reports every element of a list under a secret member that still carries a
// value. The save hook replaces such a list element by element, so the scan reads the elements
// the same way. The rest holds the line from the first element on, and the list ends at the
// first closing bracket.
func listFindings(name string, number int, member, rest string) []string {
	if end := strings.Index(rest, "]"); end >= 0 {
		rest = rest[:end]
	}
	var findings []string
	for _, element := range listElement.FindAllStringSubmatch(rest, -1) {
		if element[1] == "" || element[1] == redactionMarker {
			continue
		}
		findings = append(findings,
			fmt.Sprintf("%s line %d holds a value in the list under the %s member", name, number, member))
	}
	return findings
}

// scanCassettes reads every cassette under dir. An absent directory and an empty one both
// return no finding, because no cassette is recorded in this repository yet.
func scanCassettes(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var findings []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != cassetteSuffix {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		findings = append(findings, cassetteFindings(path, string(content))...)
	}
	return findings, nil
}

// TestTheCommittedCassettesCarryNoCredential reads the directory a person records into. A
// cassette is a file anyone can read, so a key in one is a key on the internet.
func TestTheCommittedCassettesCarryNoCredential(t *testing.T) {
	findings, err := scanCassettes(filepath.FromSlash(cassetteDirName))
	require.NoError(t, err)
	require.Emptyf(t, findings, "a committed cassette carries a credential:\n%s", strings.Join(findings, "\n"))
}

// TestTheCassetteScanRejectsAPlantedCredential plants each shape in a temporary directory. A
// scan nobody watched fail reports a clean tree whether or not the tree is clean.
func TestTheCassetteScanRejectsAPlantedCredential(t *testing.T) {
	dir := t.TempDir()

	// plantedValue has the shape of a leaked key: one long run of key characters.
	const plantedValue = "0f3a7c19b8d24e6f5a0b1c2d3e4f5061"

	clean := "version: 2\ninteractions:\n  - request:\n      headers:\n        Content-Type:\n          - text\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "clean.yaml"), []byte(clean), 0o600))
	findings, err := scanCassettes(dir)
	require.NoError(t, err)
	require.Empty(t, findings, "a cassette with no credential header should pass")

	planted := clean + "        X-Filesapi-Key:\n          - " + plantedValue + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "planted.yaml"), []byte(planted), 0o600))
	findings, err = scanCassettes(dir)
	require.NoError(t, err)
	require.Len(t, findings, 1, "the planted header should be the one finding")
	require.Contains(t, findings[0], "X-Filesapi-Key")
	require.Contains(t, findings[0], filepath.Join(dir, "planted.yaml"))

	inline := clean + "        x-api-key: " + plantedValue + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inline.yaml"), []byte(inline), 0o600))
	findings, err = scanCassettes(dir)
	require.NoError(t, err)
	require.Len(t, findings, 3, "the header and the token after it are both findings")
	require.Contains(t, strings.Join(findings, "\n"), "holds a token after the X-Api-Key header")

	// A secret in a body carries no header in front of it, so the scan reads the member names
	// as well. The two below are the shapes a remote server and a user record carry.
	secrets := clean + `      body: '{"awsSecretKey":"` + plantedValue +
		`","folder":{"password":"` + plantedValue + `"}}'` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "secrets.yaml"), []byte(secrets), 0o600))
	findings, err = scanCassettes(dir)
	require.NoError(t, err)
	require.Len(t, findings, 5, "the two planted members should be the two new findings")
	joined := strings.Join(findings, "\n")
	require.Contains(t, joined, "holds a value under the awsSecretKey member")
	require.Contains(t, joined, "holds a value under the password member")

	// A member the save hook replaced is what a committed cassette holds, and it passes.
	redacted := clean + `      body: '{"awsSecretKey":"` + redactionMarker +
		`","password":"` + redactionMarker + `"}'` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "redacted.yaml"), []byte(redacted), 0o600))
	withRedaction, err := scanCassettes(dir)
	require.NoError(t, err)
	require.Equal(t, findings, withRedaction, "a redacted member is what a committed cassette holds")

	// A file the scan does not read is not a cassette, and an absent directory is not a failure.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte(inline), 0o600))
	after, err := scanCassettes(dir)
	require.NoError(t, err)
	require.Equal(t, findings, after, "the scan should read the cassettes and nothing else")

	missing, err := scanCassettes(filepath.Join(dir, "absent"))
	require.NoError(t, err)
	require.Empty(t, missing, "an absent cassette directory should pass")

	// The save hook replaces a list under a secret member element by element, so a redacted
	// list is the shape a committed cassette carries. The two files below sit in a directory
	// of their own, so each finding below comes from the list the test names.
	lists := t.TempDir()
	redactedList := clean + `      body: '{"key":["` + redactionMarker +
		`"],"outer":{"token":[["` + redactionMarker + `"]]}}'` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(lists, "redacted-list.yaml"), []byte(redactedList), 0o600))
	quiet, err := scanCassettes(lists)
	require.NoError(t, err)
	require.Empty(t, quiet, "a redacted list is what a committed cassette holds")

	plantedList := clean + `      body: '{"key":["` + plantedValue + `"]}'` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(lists, "planted-list.yaml"), []byte(plantedList), 0o600))
	loud, err := scanCassettes(lists)
	require.NoError(t, err)
	require.Len(t, loud, 1, "the planted list element should be the one finding")
	require.Contains(t, loud[0], "holds a value in the list under the key member")
	require.Contains(t, loud[0], filepath.Join(lists, "planted-list.yaml"))
}

// TestTheCassetteScanAcceptsANullSecretMember covers the shape a Files.com API key answer carries:
// the secret member is present and null. The save hook leaves it, and the scan must not report it.
func TestTheCassetteScanAcceptsANullSecretMember(t *testing.T) {
	dir := t.TempDir()
	body := "      body: '{\"aws_secret_key\":null,\"folder\":{\"password\":null},\"key\":\"" +
		redactionMarker + "\"}'\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nulls.yaml"), []byte(body), 0o600))
	findings, err := scanCassettes(dir)
	require.NoError(t, err)
	require.Empty(t, findings, "a null member holds no value")

	valued := "      body: '{\"aws_secret_key\":\"notnull\"}'\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "valued.yaml"), []byte(valued), 0o600))
	findings, err = scanCassettes(dir)
	require.NoError(t, err)
	require.Len(t, findings, 1, "a member with a value is still a finding")
}
