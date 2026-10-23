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
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// dockerInitStubs fake dumbodb and mongosh for the entrypoint's root user
// initialization. Every call is appended to $STUB_LOG. The temporary server
// (listening on 127.0.0.1:27017) waits to be stopped; the final server records
// which init variables are still in its environment.
var dockerInitStubs = map[string]string{
	"id": `[ "$1" = "-u" ] && echo "${FAKE_UID:-1234}"`,
	"dumbodb": `case " $* " in
*" 127.0.0.1:27017 "*) echo "temp-dumbodb $*" >> "$STUB_LOG"; exec sleep 30 ;;
esac
echo "dumbodb $*" >> "$STUB_LOG"
echo "dumbodb-env$(env | grep -o '^[A-Z_]*INITDB[A-Z_]*' | sort | sed 's/^/ /' | tr -d '\n')" >> "$STUB_LOG"`,
	"mongosh": `case "$*" in
*createUser*) echo "mongosh createUser user=$INITDB_ROOT_USERNAME pass=$INITDB_ROOT_PASSWORD" >> "$STUB_LOG" ;;
*.js) for a; do db=$file; file=$a; done; echo "mongosh-js $db ${file##*/}" >> "$STUB_LOG" ;;
esac
exit 0`,
}

// runDockerInit runs the image entrypoint as a non-root user with env set and
// returns its error and the recorded calls.
func runDockerInit(t *testing.T, env map[string]string, args ...string) (error, []string) {
	t.Helper()
	return runDockerInitWithScripts(t, t.TempDir(), env, args...)
}

// dockerEntrypointWithInitDir returns the entrypoint reading init scripts from
// dir instead of /docker-entrypoint-initdb.d.
func dockerEntrypointWithInitDir(t *testing.T, dir string) string {
	t.Helper()
	script := extractDockerEntrypoint(t)
	require.Contains(t, script, "/docker-entrypoint-initdb.d")
	return strings.ReplaceAll(script, "/docker-entrypoint-initdb.d", dir)
}

// runDockerInitWithScripts is runDockerInit with init scripts read from
// scripts.
func runDockerInitWithScripts(t *testing.T, scripts string, env map[string]string, args ...string) (error, []string) {
	t.Helper()

	dir := t.TempDir()
	stubs := filepath.Join(dir, "bin")
	require.NoError(t, os.Mkdir(stubs, 0o755))
	for name, body := range dockerInitStubs {
		require.NoError(t, os.WriteFile(filepath.Join(stubs, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755))
	}
	script := filepath.Join(dir, "docker-entrypoint.sh")
	require.NoError(t, os.WriteFile(script, []byte(dockerEntrypointWithInitDir(t, scripts)), 0o755))
	log := filepath.Join(dir, "calls.log")

	cmd := exec.Command(script, args...)
	cmd.Env = []string{"PATH=" + stubs + ":/usr/bin:/bin", "STUB_LOG=" + log}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	runErr := cmd.Run()

	calls, _ := os.ReadFile(log)
	trimmed := strings.TrimSpace(string(calls))
	if trimmed == "" {
		return runErr, nil
	}
	return runErr, strings.Split(trimmed, "\n")
}

// freshDataDir returns an empty data directory; entries are created inside it.
func freshDataDir(t *testing.T, entries ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, e := range entries {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, e), 0o755))
	}
	return dir
}

// As the mongo image does, MONGO_INITDB_ROOT_USERNAME/PASSWORD (or the
// DUMBO_INITDB_ROOT_* synonyms, or their _FILE forms) create a root user on a
// fresh data directory through a temporary server on 127.0.0.1, and start the
// real server with --auth.
func TestDockerEntrypointCreatesRootUser(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "pw")
	require.NoError(t, os.WriteFile(secret, []byte("from-file\n"), 0o600))

	for name, tc := range map[string]struct {
		env      map[string]string
		wantUser string
		wantPass string
	}{
		"MongoNames":    {map[string]string{"MONGO_INITDB_ROOT_USERNAME": "root", "MONGO_INITDB_ROOT_PASSWORD": "pw"}, "root", "pw"},
		"DumboNames":    {map[string]string{"DUMBO_INITDB_ROOT_USERNAME": "root", "DUMBO_INITDB_ROOT_PASSWORD": "pw"}, "root", "pw"},
		"MixedNames":    {map[string]string{"MONGO_INITDB_ROOT_USERNAME": "root", "DUMBO_INITDB_ROOT_PASSWORD": "pw"}, "root", "pw"},
		"SameBothNames": {map[string]string{"MONGO_INITDB_ROOT_USERNAME": "root", "DUMBO_INITDB_ROOT_USERNAME": "root", "MONGO_INITDB_ROOT_PASSWORD": "pw"}, "root", "pw"},
		"PasswordFile":  {map[string]string{"MONGO_INITDB_ROOT_USERNAME": "root", "MONGO_INITDB_ROOT_PASSWORD_FILE": secret}, "root", "from-file"},
		"DumboFile":     {map[string]string{"DUMBO_INITDB_ROOT_USERNAME": "root", "DUMBO_INITDB_ROOT_PASSWORD_FILE": secret}, "root", "from-file"},
	} {
		t.Run(name, func(t *testing.T) {
			data := freshDataDir(t, ".preserved")
			err, calls := runDockerInit(t, tc.env, "--data-dir", data)
			require.NoError(t, err)
			require.Equal(t, []string{
				"temp-dumbodb --data-dir " + data + " --addr 127.0.0.1:27017",
				"mongosh createUser user=" + tc.wantUser + " pass=" + tc.wantPass,
				"dumbodb --data-dir " + data + " --addr 0.0.0.0:27017 --auth",
				"dumbodb-env",
			}, calls)
		})
	}
}

func TestDockerEntrypointRootUserRules(t *testing.T) {
	root := map[string]string{"MONGO_INITDB_ROOT_USERNAME": "root", "MONGO_INITDB_ROOT_PASSWORD": "pw"}

	t.Run("ExistingDataIsNotInitialized", func(t *testing.T) {
		data := freshDataDir(t, "admin")
		err, calls := runDockerInit(t, root, "--data-dir", data)
		require.NoError(t, err)
		require.Equal(t, []string{"dumbodb --data-dir " + data + " --addr 0.0.0.0:27017 --auth", "dumbodb-env"}, calls)
	})

	t.Run("NoVariablesNoAuth", func(t *testing.T) {
		data := freshDataDir(t)
		err, calls := runDockerInit(t, nil, "--data-dir", data)
		require.NoError(t, err)
		require.Equal(t, []string{"dumbodb --data-dir " + data + " --addr 0.0.0.0:27017", "dumbodb-env"}, calls)
	})

	t.Run("AuthNotDuplicated", func(t *testing.T) {
		data := freshDataDir(t, "admin")
		err, calls := runDockerInit(t, root, "--auth", "--data-dir", data)
		require.NoError(t, err)
		require.Equal(t, "dumbodb --auth --data-dir "+data+" --addr 0.0.0.0:27017", calls[0])
	})

	t.Run("TemporaryServerDropsClusterAndAuthFlags", func(t *testing.T) {
		data := freshDataDir(t)
		err, calls := runDockerInit(t, root,
			"--data-dir", data, "--replSet", "rs0", "--keyFile=/k", "--clusterAuthMode", "keyFile",
			"--tlsMode", "requireTLS", "--tlsCertificateKeyFile", "/c.pem", "--port", "28000", "--log-level", "debug")
		require.NoError(t, err)
		require.Equal(t, "temp-dumbodb --data-dir "+data+" --tlsCertificateKeyFile /c.pem --log-level debug --tlsMode allowTLS --addr 127.0.0.1:27017", calls[0])
		require.Equal(t, "dumbodb --data-dir "+data+" --replSet rs0 --keyFile=/k --clusterAuthMode keyFile --tlsMode requireTLS --tlsCertificateKeyFile /c.pem --port 28000 --log-level debug --addr 0.0.0.0:27017 --auth", calls[2])
	})

	for name, env := range map[string]map[string]string{
		"OnlyUsername":        {"MONGO_INITDB_ROOT_USERNAME": "root"},
		"OnlyPassword":        {"DUMBO_INITDB_ROOT_PASSWORD": "pw"},
		"ValueAndFile":        {"MONGO_INITDB_ROOT_USERNAME": "root", "MONGO_INITDB_ROOT_PASSWORD": "pw", "MONGO_INITDB_ROOT_PASSWORD_FILE": "/x"},
		"MongoAndDumboDiffer": {"MONGO_INITDB_ROOT_USERNAME": "root", "DUMBO_INITDB_ROOT_USERNAME": "other", "MONGO_INITDB_ROOT_PASSWORD": "pw"},
		"MissingPasswordFile": {"MONGO_INITDB_ROOT_USERNAME": "root", "MONGO_INITDB_ROOT_PASSWORD_FILE": "/nonexistent/pw"},
	} {
		t.Run(name, func(t *testing.T) {
			err, calls := runDockerInit(t, env, "--data-dir", freshDataDir(t))
			require.Error(t, err, "the container must not start")
			require.Empty(t, calls, "no server may start")
		})
	}
}

// TestDockerEntrypointRootUserEndToEnd runs the real entrypoint with a real
// dumbodb and mongosh (not inside Docker): the root user is created and an
// init script runs on a fresh data directory, the server then requires
// authentication, and a restart on the same data does not initialize again.
func TestDockerEntrypointRootUserEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("mongosh"); err != nil {
		t.Skip("mongosh is not installed")
	}
	if conn, err := net.Dial("tcp", "127.0.0.1:27017"); err == nil {
		conn.Close()
		t.Skip("127.0.0.1:27017, used by the temporary server, is busy")
	}

	bin := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(bin, "dumbodb"), "./cmd/dumbodb/")
	build.Dir = repoRoot()
	out, err := build.CombinedOutput()
	require.NoError(t, err, "%s", out)
	mongosh, err := exec.LookPath("mongosh")
	require.NoError(t, err)
	require.NoError(t, os.Symlink(mongosh, filepath.Join(bin, "mongosh")))

	scripts := initScripts(t, map[string]string{"01-seed.js": `db.seed.insertOne({_id: 1, from: "init"})`})
	script := filepath.Join(t.TempDir(), "docker-entrypoint.sh")
	require.NoError(t, os.WriteFile(script, []byte(dockerEntrypointWithInitDir(t, scripts)), 0o755))
	data := t.TempDir()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	listener.Close()

	start := func(env ...string) *exec.Cmd {
		cmd := exec.Command(script, "--data-dir", data, "--addr", addr)
		cmd.Env = append([]string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "DUMBODB_NO_METRICS=1"}, env...)
		require.NoError(t, cmd.Start())
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		for i := 0; i < 100; i++ {
			if conn, dialErr := net.Dial("tcp", addr); dialErr == nil {
				conn.Close()
				return cmd
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatal("the server did not start")
		return nil
	}
	seeded := func() int64 {
		uri := "mongodb://root:s3cr3t%20pw@" + addr + "/admin?directConnection=true"
		client, err := mongo.Connect(options.Client().ApplyURI(uri).SetTimeout(10 * time.Second))
		require.NoError(t, err)
		defer client.Disconnect(context.Background())
		n, err := client.Database("app").Collection("seed").CountDocuments(context.Background(), bson.D{})
		require.NoError(t, err)
		return n
	}
	ping := func(user, password string) error {
		uri := "mongodb://" + addr + "/?directConnection=true"
		if user != "" {
			uri = "mongodb://" + user + ":" + password + "@" + addr + "/admin?directConnection=true"
		}
		client, err := mongo.Connect(options.Client().ApplyURI(uri).SetTimeout(10 * time.Second))
		require.NoError(t, err)
		defer client.Disconnect(context.Background())
		return client.Database("admin").RunCommand(context.Background(), bson.D{{Key: "listDatabases", Value: 1}}).Err()
	}

	server := start("MONGO_INITDB_ROOT_USERNAME=root", "DUMBO_INITDB_ROOT_PASSWORD=s3cr3t pw", "DUMBO_INITDB_DATABASE=app")
	require.Error(t, ping("", ""), "the server must require authentication")
	require.NoError(t, ping("root", "s3cr3t pw"))
	require.Error(t, ping("root", "wrong"))
	require.EqualValues(t, 1, seeded(), "the init script must have run against DUMBO_INITDB_DATABASE")
	_ = server.Process.Signal(syscall.SIGTERM)
	_ = server.Wait()

	// Restarted on the same data with other credentials: no new user is
	// created, and authentication stays on.
	start("MONGO_INITDB_ROOT_USERNAME=other", "MONGO_INITDB_ROOT_PASSWORD=other", "DUMBO_INITDB_DATABASE=app")
	require.Error(t, ping("other", "other"))
	require.NoError(t, ping("root", "s3cr3t pw"))
	require.EqualValues(t, 1, seeded(), "init scripts must not run again")
}

// initScripts returns a directory holding init scripts with the given names
// and contents.
func initScripts(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}
	return dir
}

// As the mongo image does, /docker-entrypoint-initdb.d scripts run once, on a
// fresh data directory, against the temporary server: *.sh are sourced, *.js
// run with mongosh against MONGO_INITDB_DATABASE (DUMBO_INITDB_DATABASE),
// default "test", and anything else is ignored.
func TestDockerEntrypointRunsInitScripts(t *testing.T) {
	scripts := initScripts(t, map[string]string{
		"01-setup.sh":  `echo "sh ran db=$MONGO_INITDB_DATABASE" >> "$STUB_LOG"`,
		"02-data.js":   "db.c.insertOne({a: 1})",
		"03-notes.txt": "ignored",
	})
	root := map[string]string{"MONGO_INITDB_ROOT_USERNAME": "root", "MONGO_INITDB_ROOT_PASSWORD": "pw"}

	t.Run("AfterTheRootUserInOrder", func(t *testing.T) {
		data := freshDataDir(t)
		err, calls := runDockerInitWithScripts(t, scripts, root, "--data-dir", data)
		require.NoError(t, err)
		require.Equal(t, []string{
			"temp-dumbodb --data-dir " + data + " --addr 127.0.0.1:27017",
			"mongosh createUser user=root pass=pw",
			"sh ran db=test",
			"mongosh-js test 02-data.js",
			"dumbodb --data-dir " + data + " --addr 0.0.0.0:27017 --auth",
			"dumbodb-env",
		}, calls)
	})

	t.Run("ScriptsAloneInitializeWithoutAuth", func(t *testing.T) {
		data := freshDataDir(t)
		err, calls := runDockerInitWithScripts(t, scripts, nil, "--data-dir", data)
		require.NoError(t, err)
		require.Equal(t, []string{
			"temp-dumbodb --data-dir " + data + " --addr 127.0.0.1:27017",
			"sh ran db=test",
			"mongosh-js test 02-data.js",
			"dumbodb --data-dir " + data + " --addr 0.0.0.0:27017",
			"dumbodb-env",
		}, calls)
	})

	t.Run("DatabaseSynonym", func(t *testing.T) {
		err, calls := runDockerInitWithScripts(t, scripts, map[string]string{"DUMBO_INITDB_DATABASE": "app"}, "--data-dir", freshDataDir(t))
		require.NoError(t, err)
		require.Contains(t, calls, "mongosh-js app 02-data.js")
	})

	t.Run("NotOnExistingData", func(t *testing.T) {
		data := freshDataDir(t, "app")
		err, calls := runDockerInitWithScripts(t, scripts, nil, "--data-dir", data)
		require.NoError(t, err)
		require.Equal(t, []string{"dumbodb --data-dir " + data + " --addr 0.0.0.0:27017", "dumbodb-env"}, calls)
	})

	t.Run("OnlyIgnoredFilesDoNotInitialize", func(t *testing.T) {
		data := freshDataDir(t)
		err, calls := runDockerInitWithScripts(t, initScripts(t, map[string]string{"README": "x"}), nil, "--data-dir", data)
		require.NoError(t, err)
		require.Equal(t, []string{"dumbodb --data-dir " + data + " --addr 0.0.0.0:27017", "dumbodb-env"}, calls)
	})

	t.Run("DatabaseNamesDiffer", func(t *testing.T) {
		err, calls := runDockerInitWithScripts(t, scripts,
			map[string]string{"MONGO_INITDB_DATABASE": "a", "DUMBO_INITDB_DATABASE": "b"}, "--data-dir", freshDataDir(t))
		require.Error(t, err)
		require.Empty(t, calls)
	})
}
