package filescom

import (
	"os"
	"path/filepath"
)

// upstreamDirName is the submodule directory the upstream markdown lives under.
const upstreamDirName = "upstream"

// The bridge resolves UpstreamRepoPath against the process working directory, which differs
// between `make tfgen` (repo root) and a hand-run from provider/.
func upstreamRepoPath() string {
	for _, candidate := range []string{upstreamDirName, filepath.Join("..", upstreamDirName)} {
		if info, err := os.Stat(filepath.Join(candidate, "docs")); err == nil && info.IsDir() {
			return candidate
		}
	}
	panic("upstream submodule missing or empty: run `git submodule update --init upstream`")
}
