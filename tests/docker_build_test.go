// Copyright 2026 Dolthub, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tests

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const dockerFetchHeredoc = "COPY <<'EOF' /usr/local/bin/fetch-dumbodb.sh\n"

func dockerfileText(t *testing.T) string {
	t.Helper()
	dockerfile, err := os.ReadFile(filepath.Join("..", "docker", "Dockerfile"))
	require.NoError(t, err)
	return string(dockerfile)
}

// runDockerFetch runs the image's download script with curl served from
// files in a fixture directory and install recorded instead of performed.
// It returns the script's error and the install calls.
func runDockerFetch(t *testing.T, version string, files map[string]string) (error, []string) {
	t.Helper()

	_, rest, found := strings.Cut(dockerfileText(t), dockerFetchHeredoc)
	require.True(t, found, "docker/Dockerfile must define the download script with %q", strings.TrimSpace(dockerFetchHeredoc))
	script, _, found := strings.Cut(rest, "\nEOF\n")
	require.True(t, found, "download script heredoc is not terminated")

	dir := t.TempDir()
	fixtures := filepath.Join(dir, "fixtures")
	stubs := filepath.Join(dir, "bin")
	require.NoError(t, os.Mkdir(fixtures, 0o755))
	require.NoError(t, os.Mkdir(stubs, 0o755))
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(fixtures, name), []byte(content), 0o644))
	}

	// curl -fsSL [-o out] <url>: serves the fixture named by the URL's last
	// path element, failing like curl -f when it does not exist.
	curl := `out=""
while [ $# -gt 1 ]; do case "$1" in -o) out="$2"; shift 2;; *) shift;; esac; done
src="$FIXTURES/${1##*/}"
[ -f "$src" ] || exit 22
if [ -n "$out" ]; then cp "$src" "$out"; else cat "$src"; fi`
	install := `echo "install $*" >> "$STUB_LOG"`
	for name, body := range map[string]string{"curl": curl, "install": install} {
		require.NoError(t, os.WriteFile(filepath.Join(stubs, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755))
	}
	scriptPath := filepath.Join(dir, "fetch-dumbodb.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte(script+"\n"), 0o755))

	log := filepath.Join(dir, "install.log")
	cmd := exec.Command(scriptPath)
	cmd.Dir = dir
	cmd.Env = []string{
		"PATH=" + stubs + ":/usr/bin:/bin", "STUB_LOG=" + log, "FIXTURES=" + fixtures,
		"DUMBODB_VERSION=" + version, "TARGETARCH=amd64",
	}
	runErr := cmd.Run()

	calls, _ := os.ReadFile(log)
	return runErr, strings.Fields(strings.TrimSpace(string(calls)))
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// The image used to install whatever binary the download returned. It now
// checks it against the release's SHA256SUMS and refuses a mismatch or a
// missing checksum.
func TestDockerImageVerifiesDownloadedBinary(t *testing.T) {
	binary := "dumbodb binary"
	sums := sha256Hex(binary) + "  dumbodb-linux-amd64\n" + sha256Hex("other") + "  dumbodb-linux-arm64\n"

	t.Run("MatchingChecksumInstalls", func(t *testing.T) {
		err, calls := runDockerFetch(t, "1.2.3", map[string]string{"dumbodb-linux-amd64": binary, "SHA256SUMS": sums})
		require.NoError(t, err)
		require.Contains(t, strings.Join(calls, " "), "/usr/local/bin/dumbodb")
	})

	t.Run("LatestResolvesTagAndVerifies", func(t *testing.T) {
		err, calls := runDockerFetch(t, "latest", map[string]string{
			"latest": `{"tag_name": "v1.2.3"}`, "dumbodb-linux-amd64": binary, "SHA256SUMS": sums,
		})
		require.NoError(t, err)
		require.NotEmpty(t, calls)
	})

	for name, files := range map[string]map[string]string{
		"TamperedBinary":   {"dumbodb-linux-amd64": "tampered", "SHA256SUMS": sums},
		"MissingChecksums": {"dumbodb-linux-amd64": binary},
		"NoEntryForArch":   {"dumbodb-linux-amd64": binary, "SHA256SUMS": sha256Hex("other") + "  dumbodb-linux-arm64\n"},
	} {
		t.Run(name, func(t *testing.T) {
			err, calls := runDockerFetch(t, "1.2.3", files)
			require.Error(t, err, "the build must fail")
			require.Empty(t, calls, "nothing may be installed")
		})
	}

	t.Run("SourceBuildDownloadsNothing", func(t *testing.T) {
		err, calls := runDockerFetch(t, "source", nil)
		require.NoError(t, err)
		require.Empty(t, calls)
	})
}

// curl is only needed to download the release; the runtime image should not
// carry it.
func TestDockerRuntimeImageHasNoCurl(t *testing.T) {
	var instructions []string
	for _, line := range strings.Split(dockerfileText(t), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			instructions = append(instructions, line)
		}
	}
	dockerfile := strings.Join(instructions, "\n")
	_, runtime, found := strings.Cut(dockerfile, "FROM base AS runtime")
	require.True(t, found)
	base, _, _ := strings.Cut(dockerfile, "FROM ubuntu:24.04 AS build-from-source")
	require.NotContains(t, base, "curl", "the base stage the runtime image builds on installs curl")
	require.NotContains(t, runtime, "curl", "the runtime stage installs curl")
}

// The release must publish the checksums the image verifies.
func TestReleasePublishesChecksums(t *testing.T) {
	workflow, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "release.yml"))
	require.NoError(t, err)
	require.Contains(t, string(workflow), "sha256sum dumbodb-* > SHA256SUMS")
	require.Contains(t, string(workflow), "dist/SHA256SUMS")
}
