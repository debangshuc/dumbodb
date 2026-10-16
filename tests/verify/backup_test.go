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

package verify

// TestBackupVerify is the automated analog of docs/verify/backup.md: back up
// a database on one server and restore it on another.
//
// TestBackupActions covers every dumboBackup action on a single server. Its
// subtests run sequentially, sharing one server and backup directory so each
// step's side effects carry into the next.

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

const codeOperationFailed = 96

func backupCmd(fields ...bson.E) bson.D {
	return append(bson.D{{Key: "dumboBackup", Value: int32(1)}}, fields...)
}

func runBackup(t *testing.T, c *mongo.Client, fields ...bson.E) bson.M {
	t.Helper()
	var res bson.M
	require.NoError(t, c.Database("admin").RunCommand(context.Background(), backupCmd(fields...)).Decode(&res))
	require.EqualValues(t, 1, res["ok"])
	return res
}

func backupErr(c *mongo.Client, fields ...bson.E) error {
	return c.Database("admin").RunCommand(context.Background(), backupCmd(fields...)).Err()
}

func countIn(t *testing.T, c *mongo.Client, db, coll string) int64 {
	t.Helper()
	n, err := c.Database(db).Collection(coll).CountDocuments(context.Background(), bson.D{})
	require.NoError(t, err)
	return n
}

func e(key string, value any) bson.E {
	return bson.E{Key: key, Value: value}
}

func TestBackupVerify(t *testing.T) {
	source := startDumboDB(t).Client
	target := startDumboDB(t).Client
	ctx := context.Background()
	url := "file://" + filepath.Join(t.TempDir(), "orders")

	db := source.Database("orders")
	_, err := db.Collection("items").InsertMany(ctx, []any{bson.D{{Key: "_id", Value: 1}}, bson.D{{Key: "_id", Value: 2}}})
	require.NoError(t, err)
	require.NoError(t, db.RunCommand(ctx, bson.D{{Key: "dumboCommit", Value: 1}, {Key: "message", Value: "first two"}}).Err())
	require.NoError(t, db.RunCommand(ctx, bson.D{{Key: "dumboBranch", Value: 1}, {Key: "action", Value: "add"}, {Key: "branch", Value: "feature"}}).Err())
	_, err = db.Collection("items").InsertOne(ctx, bson.D{{Key: "_id", Value: 3}})
	require.NoError(t, err)

	runBackup(t, source, e("action", "add"), e("db", "orders"), e("name", "offsite"), e("url", url))
	synced := runBackup(t, source, e("action", "sync"), e("db", "orders"), e("name", "offsite"))
	require.Equal(t, false, synced["upToDate"])

	restored := runBackup(t, target, e("action", "restore"), e("url", url), e("as", "orders"))
	require.Equal(t, synced["root"], restored["root"])

	require.EqualValues(t, 3, countIn(t, target, "orders", "items"), "the uncommitted insert is restored")
	require.EqualValues(t, 2, countIn(t, target, "orders@feature", "items"))
	var status bson.M
	require.NoError(t, target.Database("orders").RunCommand(ctx, bson.D{{Key: "dumboStatus", Value: 1}}).Decode(&status))
	require.Equal(t, true, status["dirty"], "%v", status)
}

func TestBackupActions(t *testing.T) {
	env := startDumboDB(t)
	c := env.Client
	ctx := context.Background()
	dir := t.TempDir()
	nightly := "file://" + filepath.Join(dir, "nightly")
	adhoc := "file://" + filepath.Join(dir, "adhoc")

	t.Run("Setup", func(t *testing.T) {
		db := c.Database("backupvdb")
		_, err := db.Collection("items").InsertOne(ctx, bson.D{{Key: "_id", Value: 1}, {Key: "label", Value: "committed"}})
		require.NoError(t, err)
		require.NoError(t, db.RunCommand(ctx, bson.D{{Key: "dumboCommit", Value: 1}, {Key: "message", Value: "c1"}}).Err())
		require.NoError(t, db.RunCommand(ctx, bson.D{{Key: "dumboTag", Value: 1}, {Key: "name", Value: "v1"}}).Err())
		require.NoError(t, db.RunCommand(ctx, bson.D{{Key: "dumboBranch", Value: 1}, {Key: "action", Value: "add"}, {Key: "branch", Value: "feature"}}).Err())
		_, err = c.Database("backupvdb@feature").Collection("items").InsertOne(ctx, bson.D{{Key: "_id", Value: 10}, {Key: "label", Value: "feature"}})
		require.NoError(t, err)
		require.NoError(t, c.Database("backupvdb@feature").RunCommand(ctx, bson.D{{Key: "dumboCommit", Value: 1}, {Key: "message", Value: "f1"}}).Err())
		_, err = db.Collection("items").InsertOne(ctx, bson.D{{Key: "_id", Value: 2}, {Key: "label", Value: "uncommitted"}})
		require.NoError(t, err)
		require.NoError(t, db.RunCommand(ctx, bson.D{{Key: "dumboRemote", Value: 1}, {Key: "action", Value: "add"},
			{Key: "name", Value: "origin"}, {Key: "url", Value: "file://" + filepath.Join(dir, "origin")}}).Err())
	})

	t.Run("Scenario1_AddAndList", func(t *testing.T) {
		res := runBackup(t, c, e("action", "add"), e("db", "backupvdb"), e("name", "nightly"), e("url", nightly))
		require.Equal(t, "nightly", res["name"])
		require.Equal(t, nightly, res["url"])

		res = runBackup(t, c, e("action", "list"), e("db", "backupvdb"))
		backups := res["backups"].(bson.A)
		require.Len(t, backups, 1)
		require.Equal(t, "nightly", backups[0].(bson.M)["name"])
	})

	var firstRoot string
	t.Run("Scenario2_Sync", func(t *testing.T) {
		res := runBackup(t, c, e("action", "sync"), e("db", "backupvdb"), e("name", "nightly"))
		require.Equal(t, false, res["upToDate"])
		require.Equal(t, nightly, res["url"])
		firstRoot, _ = res["root"].(string)
		require.NotEmpty(t, firstRoot)

		res = runBackup(t, c, e("action", "sync"), e("db", "backupvdb"), e("name", "nightly"))
		require.Equal(t, true, res["upToDate"])
		require.Equal(t, firstRoot, res["root"])
	})

	t.Run("Scenario3_Restore", func(t *testing.T) {
		res := runBackup(t, c, e("action", "restore"), e("url", nightly), e("as", "backupvdb_copy"))
		require.Equal(t, "backupvdb_copy", res["db"])
		require.Equal(t, firstRoot, res["root"])

		require.EqualValues(t, 2, countIn(t, c, "backupvdb_copy", "items"), "the uncommitted doc is restored")
		require.EqualValues(t, 2, countIn(t, c, "backupvdb_copy@feature", "items"))

		var tags bson.M
		require.NoError(t, c.Database("backupvdb_copy").RunCommand(ctx, bson.D{{Key: "dumboTag", Value: 1}}).Decode(&tags))
		require.Len(t, tags["tags"].(bson.A), 1)

		var status bson.M
		require.NoError(t, c.Database("backupvdb_copy").RunCommand(ctx, bson.D{{Key: "dumboStatus", Value: 1}}).Decode(&status))
		require.Equal(t, true, status["dirty"], "the restored working set is still dirty: %v", status)

		var remotes bson.M
		require.NoError(t, c.Database("backupvdb_copy").RunCommand(ctx, bson.D{{Key: "dumboRemote", Value: 1}, {Key: "action", Value: "list"}}).Decode(&remotes))
		require.Empty(t, remotes["remotes"], "remotes live in admin and are not restored")
		res = runBackup(t, c, e("action", "list"), e("db", "backupvdb_copy"))
		require.Empty(t, res["backups"])
	})

	t.Run("Scenario4_RestoreOverExisting", func(t *testing.T) {
		_, err := c.Database("backupvdb_copy").Collection("items").InsertOne(ctx, bson.D{{Key: "_id", Value: 3}})
		require.NoError(t, err)

		err = backupErr(c, e("action", "restore"), e("url", nightly), e("as", "backupvdb_copy"))
		requireCode(t, err, codeOperationFailed)
		require.ErrorContains(t, err, "already exists")
		require.EqualValues(t, 3, countIn(t, c, "backupvdb_copy", "items"))

		runBackup(t, c, e("action", "restore"), e("url", nightly), e("as", "backupvdb_copy"), e("force", true))
		require.EqualValues(t, 2, countIn(t, c, "backupvdb_copy", "items"))

		var dropped bson.M
		require.NoError(t, c.Database("admin").RunCommand(ctx, bson.D{{Key: "dumboUndrop", Value: 1}}).Decode(&dropped))
		found := false
		for _, d := range dropped["dropped"].(bson.A) {
			if d.(bson.M)["name"] == "backupvdb_copy" {
				found = true
			}
		}
		require.True(t, found, "the replaced database can be undropped: %v", dropped)
	})

	t.Run("Scenario5_IncrementalSync", func(t *testing.T) {
		_, err := c.Database("backupvdb").Collection("items").InsertOne(ctx, bson.D{{Key: "_id", Value: 4}})
		require.NoError(t, err)
		res := runBackup(t, c, e("action", "sync"), e("db", "backupvdb"), e("name", "nightly"))
		require.Equal(t, false, res["upToDate"])
		require.NotEqual(t, firstRoot, res["root"])

		runBackup(t, c, e("action", "restore"), e("url", nightly), e("as", "backupvdb_copy"), e("force", true))
		require.EqualValues(t, 3, countIn(t, c, "backupvdb_copy", "items"))
	})

	t.Run("Scenario6_SyncURL", func(t *testing.T) {
		res := runBackup(t, c, e("action", "sync-url"), e("db", "backupvdb"), e("url", adhoc))
		require.Equal(t, adhoc, res["url"])
		runBackup(t, c, e("action", "restore"), e("url", adhoc), e("as", "backupvdb_adhoc"))
		require.EqualValues(t, 3, countIn(t, c, "backupvdb_adhoc", "items"))
	})

	t.Run("Scenario7_RemoveKeepsData", func(t *testing.T) {
		runBackup(t, c, e("action", "remove"), e("db", "backupvdb"), e("name", "nightly"))
		res := runBackup(t, c, e("action", "list"), e("db", "backupvdb"))
		require.Empty(t, res["backups"])

		runBackup(t, c, e("action", "restore"), e("url", nightly), e("as", "backupvdb_after_remove"))
		require.EqualValues(t, 3, countIn(t, c, "backupvdb_after_remove", "items"))
	})

	t.Run("Scenario8_Errors", func(t *testing.T) {
		requireCode(t, runIn(c, "backupvdb", backupCmd(e("action", "list"), e("db", "backupvdb"))), codeOperationFailed)

		for _, fields := range [][]bson.E{
			{e("action", "add"), e("db", "admin"), e("name", "x"), e("url", "file://"+filepath.Join(dir, "admin"))},
			{e("action", "sync-url"), e("db", "admin"), e("url", "file://"+filepath.Join(dir, "admin"))},
			{e("action", "restore"), e("url", adhoc), e("as", "admin"), e("force", true)},
			{e("action", "sync-url"), e("db", "backupvdb"), e("url", "file://"+filepath.Join(dir, "origin"))},
			{e("action", "sync"), e("db", "backupvdb"), e("name", "nosuch")},
			{e("action", "sync-url"), e("db", "nosuchdb"), e("url", "file://"+filepath.Join(dir, "none"))},
		} {
			requireCode(t, backupErr(c, fields...), codeOperationFailed)
		}

		for _, fields := range [][]bson.E{
			{e("action", "bogus")},
			{e("action", "add"), e("db", "backupvdb"), e("name", "x")},
			{e("action", "sync")},
			{e("action", "restore"), e("url", adhoc)},
		} {
			requireCode(t, backupErr(c, fields...), codeBadValue)
		}

		requireCode(t, backupErr(c, e("action", "list"), e("db", "backupvdb"), e("bogus", 1)), codeUnknownField)

		var res bson.M
		require.NoError(t, c.Database("admin").RunCommand(ctx, bson.D{{Key: "listDatabases", Value: 1}, {Key: "nameOnly", Value: true}}).Decode(&res))
		for _, d := range res["databases"].(bson.A) {
			require.NotEqual(t, "nosuchdb", d.(bson.M)["name"])
		}
	})
}

// Backups use the server's own filesystem access and credentials, so like
// push, pull, fetch and clone they are refused for every user under --auth.
func TestBackupDisabledUnderAuth(t *testing.T) {
	env := startDumboDB(t, "--auth")
	require.NoError(t, env.Client.Database("admin").RunCommand(context.Background(), bson.D{
		{Key: "createUser", Value: "admin"}, {Key: "pwd", Value: "admin-pw"},
		{Key: "roles", Value: bson.A{bson.D{{Key: "role", Value: "root"}, {Key: "db", Value: "admin"}}}},
	}).Err())
	root := authClient(t, env.Port, "admin", "admin-pw", "admin")
	require.NoError(t, insert(root, "appdb", "things", bson.D{{Key: "_id", Value: 1}}))

	url := "file://" + filepath.Join(t.TempDir(), "bk")
	for _, fields := range [][]bson.E{
		{e("action", "add"), e("db", "appdb"), e("name", "nightly"), e("url", url)},
		{e("action", "list"), e("db", "appdb")},
		{e("action", "remove"), e("db", "appdb"), e("name", "nightly")},
		{e("action", "sync"), e("db", "appdb"), e("name", "nightly")},
		{e("action", "sync-url"), e("db", "appdb"), e("url", url)},
		{e("action", "restore"), e("url", url), e("as", "copy")},
	} {
		requireCode(t, backupErr(root, fields...), codeUnauthorized)
		requireCode(t, root.Database("admin").RunCommand(context.Background(),
			append(bson.D{{Key: "doltBackup", Value: int32(1)}}, fields...)).Err(), codeUnauthorized)
	}
}
