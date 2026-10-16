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
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dolthub/dumbodb/internal/backends"
	"github.com/dolthub/dumbodb/internal/types"
	"github.com/dolthub/dumbodb/internal/util/iterator"
	"github.com/dolthub/dumbodb/internal/util/must"
)

// backupsCollection is the admin-database collection that stores backup
// definitions, one document per backup keyed by "<db>.<backup>".
const backupsCollection = "system.backups"

// DumboDBBackup manages Dolt-style backups of one database. Backup
// definitions live in admin.system.backups, parallel to admin.system.remotes.
// The admin database itself can never be backed up: it holds every user's
// SCRAM keys.
func (b *Backend) DumboDBBackup(ctx context.Context, params *backends.BackupParams) (*backends.BackupResult, error) {
	if err := b.validateBackupSourceDB(params.DBName); err != nil {
		return nil, err
	}

	coll, err := b.adminCollection(backupsCollection)
	if err != nil {
		return nil, err
	}

	switch params.Action {
	case "add":
		return b.backupAdd(ctx, coll, params)
	case "list":
		return b.backupList(ctx, coll, params)
	case "remove":
		return b.backupRemove(ctx, coll, params)
	default:
		return nil, fmt.Errorf("dumboBackup: unknown action %q (expected add, list, or remove)", params.Action)
	}
}

func (b *Backend) validateBackupSourceDB(dbName string) error {
	if dbName == "" {
		return fmt.Errorf("dumboBackup: database name is required")
	}
	if !backends.ValidDatabaseDirName(dbName) {
		return fmt.Errorf("dumboBackup: invalid database name %q", dbName)
	}
	if dbName == "admin" || isReservedDatabase(dbName) {
		return fmt.Errorf("dumboBackup: the %q database cannot be backed up", dbName)
	}
	return nil
}

func (b *Backend) adminCollection(name string) (backends.Collection, error) {
	adminDB, err := b.Database("admin")
	if err != nil {
		return nil, err
	}
	return adminDB.Collection(name)
}

func (b *Backend) backupAdd(ctx context.Context, coll backends.Collection, params *backends.BackupParams) (*backends.BackupResult, error) {
	if err := validateConfigName("dumboBackup", "backup", params.Name); err != nil {
		return nil, err
	}

	if _, err := os.Stat(filepath.Join(b.dataDir, params.DBName)); err != nil {
		return nil, fmt.Errorf("dumboBackup add: database %q does not exist", params.DBName)
	}

	ru, err := b.parseRemoteURL(params.URL)
	if err != nil {
		return nil, fmt.Errorf("dumboBackup add: %w", err)
	}
	if !isCloneableScheme(ru.Scheme) {
		return nil, fmt.Errorf("dumboBackup add: unsupported backup scheme %q", ru.Scheme)
	}

	id := remoteID(params.DBName, params.Name)
	existing, err := findRemoteDoc(ctx, coll, id)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("dumboBackup add: backup %q already exists for database %q", params.Name, params.DBName)
	}

	if err = b.checkBackupURLConflict(ctx, ru.Raw); err != nil {
		return nil, err
	}

	doc := must.NotFail(types.NewDocument(
		"_id", id,
		"name", params.Name,
		"db", params.DBName,
		"url", ru.Raw,
	))
	if _, err = coll.InsertAll(ctx, &backends.InsertAllParams{Docs: []*types.Document{doc}}); err != nil {
		return nil, err
	}

	return &backends.BackupResult{Backups: []backends.BackupInfo{{Name: params.Name, URL: ru.Raw}}}, nil
}

// checkBackupURLConflict refuses a backup URL already used by any remote or
// backup of any database. A backup sync replaces the destination's root, so a
// shared URL would destroy the other remote or backup.
func (b *Backend) checkBackupURLConflict(ctx context.Context, url string) error {
	for _, collName := range []string{remotesCollection, backupsCollection} {
		coll, err := b.adminCollection(collName)
		if err != nil {
			return err
		}
		docs, err := queryConfigDocs(ctx, coll, nil)
		if err != nil {
			return err
		}
		for _, doc := range docs {
			if u, _ := doc.Get("url"); u == url {
				id, _ := doc.Get("_id")
				return fmt.Errorf("dumboBackup add: url %q is already used by %s %q", url, configKind(collName), id)
			}
		}
	}
	return nil
}

func configKind(collName string) string {
	if collName == backupsCollection {
		return "backup"
	}
	return "remote"
}

func (b *Backend) backupList(ctx context.Context, coll backends.Collection, params *backends.BackupParams) (*backends.BackupResult, error) {
	docs, err := queryConfigDocs(ctx, coll, must.NotFail(types.NewDocument("db", params.DBName)))
	if err != nil {
		return nil, err
	}

	out := []backends.BackupInfo{}
	for _, doc := range docs {
		// The backend filter may be a superset; re-check the db field.
		if dbv, _ := doc.Get("db"); dbv != params.DBName {
			continue
		}
		name, _ := doc.Get("name")
		url, _ := doc.Get("url")
		ns, _ := name.(string)
		us, _ := url.(string)
		out = append(out, backends.BackupInfo{Name: ns, URL: us})
	}

	return &backends.BackupResult{Backups: out}, nil
}

// backupRemove deletes the backup definition only; data already synced to the
// backup URL is left in place.
func (b *Backend) backupRemove(ctx context.Context, coll backends.Collection, params *backends.BackupParams) (*backends.BackupResult, error) {
	if params.Name == "" {
		return nil, fmt.Errorf("dumboBackup remove: backup name is required")
	}

	res, err := coll.DeleteAll(ctx, &backends.DeleteAllParams{
		IDs: []any{remoteID(params.DBName, params.Name)},
	})
	if err != nil {
		return nil, err
	}
	if res.Deleted == 0 {
		return nil, fmt.Errorf("dumboBackup remove: backup %q not found for database %q", params.Name, params.DBName)
	}

	return &backends.BackupResult{}, nil
}

// queryConfigDocs returns every document in coll matching filter (nil for all).
func queryConfigDocs(ctx context.Context, coll backends.Collection, filter *types.Document) ([]*types.Document, error) {
	qr, err := coll.Query(ctx, &backends.QueryParams{Filter: filter})
	if err != nil {
		return nil, err
	}
	defer qr.Iter.Close()

	var docs []*types.Document
	for {
		_, doc, err := qr.Iter.Next()
		if errors.Is(err, iterator.ErrIteratorDone) {
			return docs, nil
		}
		if err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
}
