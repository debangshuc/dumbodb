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

package dolt

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dolthub/dumbodb/internal/backends"
)

func backupParams(db, action, name, url string) *backends.BackupParams {
	return &backends.BackupParams{DBName: db, Action: action, Name: name, URL: url}
}

func TestDumboDBBackupRegistry(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)
	insertDoc(t, b, "mydb", "coll", mustDoc(t, "_id", int64(1)))
	insertDoc(t, b, "otherdb", "coll", mustDoc(t, "_id", int64(1)))
	url := "file://" + filepath.Join(t.TempDir(), "mydb")

	res, err := b.DumboDBBackup(ctx, backupParams("mydb", "add", "nightly", url))
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(res.Backups) != 1 || res.Backups[0].Name != "nightly" || res.Backups[0].URL != url {
		t.Fatalf("add result = %+v", res)
	}

	res, err = b.DumboDBBackup(ctx, backupParams("mydb", "list", "", ""))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(res.Backups) != 1 || res.Backups[0].Name != "nightly" || res.Backups[0].URL != url {
		t.Fatalf("list = %+v", res)
	}

	res, err = b.DumboDBBackup(ctx, backupParams("otherdb", "list", "", ""))
	if err != nil {
		t.Fatalf("otherdb list: %v", err)
	}
	if len(res.Backups) != 0 {
		t.Errorf("otherdb list = %+v, want empty", res.Backups)
	}

	remotes, err := b.DumboDBRemote(ctx, &backends.RemoteParams{DBName: "mydb", Action: "list"})
	if err != nil {
		t.Fatalf("remote list: %v", err)
	}
	if len(remotes.Remotes) != 0 {
		t.Errorf("backup leaked into remotes: %+v", remotes.Remotes)
	}

	if _, err = b.DumboDBBackup(ctx, backupParams("mydb", "remove", "nightly", "")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	res, err = b.DumboDBBackup(ctx, backupParams("mydb", "list", "", ""))
	if err != nil {
		t.Fatalf("list after remove: %v", err)
	}
	if len(res.Backups) != 0 {
		t.Errorf("list after remove = %+v, want empty", res.Backups)
	}

	if _, err = b.DumboDBBackup(ctx, backupParams("mydb", "remove", "nightly", "")); err == nil {
		t.Error("remove missing backup: want error, got nil")
	}
}

func TestDumboDBBackupAddRejects(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)
	insertDoc(t, b, "mydb", "coll", mustDoc(t, "_id", int64(1)))
	insertDoc(t, b, "otherdb", "coll", mustDoc(t, "_id", int64(1)))
	dir := t.TempDir()
	used := "file://" + filepath.Join(dir, "used")
	remoteURL := "file://" + filepath.Join(dir, "remote")
	fresh := "file://" + filepath.Join(dir, "fresh")

	if _, err := b.DumboDBBackup(ctx, backupParams("mydb", "add", "nightly", used)); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := b.DumboDBRemote(ctx, &backends.RemoteParams{DBName: "otherdb", Action: "add", Name: "origin", URL: remoteURL}); err != nil {
		t.Fatalf("remote add: %v", err)
	}

	for _, tc := range []struct {
		desc   string
		params *backends.BackupParams
	}{
		{"duplicate name", backupParams("mydb", "add", "nightly", fresh)},
		{"url used by a backup of the same db", backupParams("mydb", "add", "other", used)},
		{"url used by a backup of another db", backupParams("otherdb", "add", "nightly", used)},
		{"url used by a remote", backupParams("mydb", "add", "other", remoteURL)},
		{"unparseable url", backupParams("mydb", "add", "bad", "://x")},
		{"mem url", backupParams("mydb", "add", "mem", "mem://x")},
		{"url inside the data dir", backupParams("mydb", "add", "inside", "file://"+filepath.Join(b.dataDir, "loot"))},
		{"name with @", backupParams("mydb", "add", "a@b", fresh)},
		{"name with whitespace", backupParams("mydb", "add", "a b", fresh)},
		{"empty name", backupParams("mydb", "add", "", fresh)},
		{"missing database", backupParams("nosuchdb", "add", "nightly", fresh)},
		{"admin database", backupParams("admin", "add", "nightly", fresh)},
		{"local database", backupParams("local", "add", "nightly", fresh)},
		{"empty database", backupParams("", "add", "nightly", fresh)},
		{"traversal database", backupParams("../mydb", "add", "nightly", fresh)},
	} {
		if _, err := b.DumboDBBackup(ctx, tc.params); err == nil {
			t.Errorf("%s: want error, got nil", tc.desc)
		}
	}

	if _, err := b.DumboDBBackup(ctx, backupParams("admin", "list", "", "")); err == nil {
		t.Error("admin list: want error, got nil")
	}

	res, err := b.DumboDBBackup(ctx, backupParams("mydb", "list", "", ""))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(res.Backups) != 1 {
		t.Errorf("rejected adds were stored: %+v", res.Backups)
	}
}
