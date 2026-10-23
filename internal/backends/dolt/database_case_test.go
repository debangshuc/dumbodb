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
	"testing"

	"github.com/dolthub/dumbodb/internal/backends"
)

// On a case-insensitive filesystem "ADMIN" resolves to the admin directory,
// so before the fix it could open a second handle on admin's store or drop it.
// As in MongoDB, a database name may not differ only in case from an existing
// database or a system database.
func TestDatabaseNamesDifferingOnlyInCase(t *testing.T) {
	b := newTestBackend(t)
	ctx := context.Background()

	createCollection := func(db string) error {
		database, err := b.Database(db)
		if err != nil {
			return err
		}
		return database.CreateCollection(ctx, &backends.CreateCollectionParams{Name: "c"})
	}

	if err := createCollection("admin"); err != nil {
		t.Fatal(err)
	}
	if err := createCollection("Shop"); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"ADMIN", "Admin", "shop", "SHOP", "Local", "CONFIG"} {
		if err := createCollection(name); !backends.ErrorCodeIs(err, backends.ErrorCodeDatabaseDifferCase) {
			t.Errorf("creating a collection in %q: err = %v, want DatabaseDifferCase", name, err)
		}

		database, err := b.Database(name)
		if err != nil {
			t.Fatal(err)
		}
		list, err := database.ListCollections(ctx, nil)
		if err != nil {
			t.Fatalf("listing %q: %v", name, err)
		}
		if len(list.Collections) != 0 {
			t.Errorf("%q listed %d collections of a differently cased database", name, len(list.Collections))
		}

		if err := b.DropDatabase(ctx, &backends.DropDatabaseParams{Name: name}); !backends.ErrorCodeIs(err, backends.ErrorCodeDatabaseDoesNotExist) {
			t.Errorf("dropping %q: err = %v, want DatabaseDoesNotExist", name, err)
		}
	}

	if _, ok := b.dbs["ADMIN"]; ok {
		t.Error("a handle was opened under a differently cased name")
	}
	admin, err := b.Database("admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.CreateCollection(ctx, &backends.CreateCollectionParams{Name: "d"}); err != nil {
		t.Errorf("admin must still be usable: %v", err)
	}
}
