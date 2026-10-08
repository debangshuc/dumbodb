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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const dockerEntrypointHeredoc = "COPY <<'EOF' /usr/local/bin/docker-entrypoint.sh\n"

// dockerEntrypointStubs fake the commands the image entrypoint uses, so its
// root-to-dumbodb handoff can be checked without Docker or root. Each stub
// appends its invocation to $STUB_LOG; id reports $FAKE_UID, and setpriv
// switches it to 999 before running its command.
var dockerEntrypointStubs = map[string]string{
	"id":      `[ "$1" = "-u" ] && echo "${FAKE_UID:-0}"`,
	"find":    `echo "find $*" >> "$STUB_LOG"`,
	"chown":   `echo "chown $*" >> "$STUB_LOG"`,
	"dumbodb": `echo "dumbodb uid=$(id -u) $*" >> "$STUB_LOG"`,
	"setpriv": `echo "setpriv $*" >> "$STUB_LOG"
while [ "${1#--}" != "$1" ]; do shift; done
FAKE_UID=999 exec "$@"`,
}

func extractDockerEntrypoint(t *testing.T) string {
	t.Helper()

	dockerfile, err := os.ReadFile(filepath.Join("..", "docker", "Dockerfile"))
	require.NoError(t, err)

	_, rest, found := strings.Cut(string(dockerfile), dockerEntrypointHeredoc)
	require.True(t, found, "docker/Dockerfile must define the entrypoint with %q", strings.TrimSpace(dockerEntrypointHeredoc))
	script, _, found := strings.Cut(rest, "\nEOF\n")
	require.True(t, found, "entrypoint heredoc is not terminated")

	return script + "\n"
}

func runDockerEntrypoint(t *testing.T, uid string, args ...string) []string {
	t.Helper()

	dir := t.TempDir()
	stubs := filepath.Join(dir, "bin")
	require.NoError(t, os.Mkdir(stubs, 0o755))
	for name, body := range dockerEntrypointStubs {
		require.NoError(t, os.WriteFile(filepath.Join(stubs, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755))
	}
	script := filepath.Join(dir, "docker-entrypoint.sh")
	require.NoError(t, os.WriteFile(script, []byte(extractDockerEntrypoint(t)), 0o755))
	log := filepath.Join(dir, "calls.log")

	cmd := exec.Command(script, args...)
	cmd.Env = []string{"PATH=" + stubs + ":/usr/bin:/bin", "STUB_LOG=" + log, "FAKE_UID=" + uid}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "entrypoint failed: %s", out)

	calls, err := os.ReadFile(log)
	require.NoError(t, err)
	return strings.Split(strings.TrimSpace(string(calls)), "\n")
}

func TestDockerImageRunsServerAsNonRoot(t *testing.T) {
	t.Run("RootStartFixesOwnershipThenDropsToDumbodb", func(t *testing.T) {
		calls := runDockerEntrypoint(t, "0", "--data-dir", "/var/lib/dumbodb")
		require.Equal(t, "find -L /var/lib/dumbodb ! -user dumbodb -exec chown dumbodb:dumbodb {} +", calls[0])
		require.True(t, strings.HasPrefix(calls[len(calls)-2], "setpriv --reuid=dumbodb --regid=dumbodb --init-groups "), calls[len(calls)-2])
		require.Equal(t, "dumbodb uid=999 --data-dir /var/lib/dumbodb", calls[len(calls)-1])
	})

	t.Run("NonRootStartRunsServerDirectly", func(t *testing.T) {
		calls := runDockerEntrypoint(t, "1234", "--auth")
		require.Equal(t, []string{"dumbodb uid=1234 --auth"}, calls)
	})

	t.Run("OtherCommandsRunUnchanged", func(t *testing.T) {
		calls := runDockerEntrypoint(t, "0", "chown", "probe")
		require.Equal(t, []string{"chown probe"}, calls)
	})

	t.Run("DockerfileUsesEntrypoint", func(t *testing.T) {
		dockerfile, err := os.ReadFile(filepath.Join("..", "docker", "Dockerfile"))
		require.NoError(t, err)
		require.Contains(t, string(dockerfile), `ENTRYPOINT ["tini", "--", "/usr/local/bin/docker-entrypoint.sh"]`)
		require.Contains(t, string(dockerfile), "useradd --uid 999 --system --gid dumbodb")
		require.NotContains(t, string(dockerfile), "\nUSER ", "the entrypoint must start as root to fix data directory ownership")
	})
}
