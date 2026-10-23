// Copyright 2021 FerretDB Inc.
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

package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dolthub/dumbodb/internal/backends"
	"github.com/dolthub/dumbodb/internal/clientconn/conninfo"
	"github.com/dolthub/dumbodb/internal/handler/handlererrors"
	"github.com/dolthub/dumbodb/internal/types"
	"github.com/dolthub/dumbodb/internal/util/must"
)

func renameNamespaceMsg(from, to string) *types.Document {
	return must.NotFail(types.NewDocument("renameCollection", from, "to", to, "$db", "admin"))
}

// Collection names may contain dots, and parsing them must not let
// renameCollection reach system collections MongoDB protects.
func TestRenameCollectionDottedNames(t *testing.T) {
	h := handlerForTest(t)
	ctx := conninfo.Ctx(context.Background(), conninfo.New())
	x, err := h.b.Database("x")
	require.NoError(t, err)
	for _, name := range []string{"a.b", "plain"} {
		require.NoError(t, x.CreateCollection(ctx, &backends.CreateCollectionParams{Name: name}))
	}

	rename := func(from, to string) error {
		_, err := h.MsgRenameCollection(ctx, must.NotFail(documentOpMsg(renameNamespaceMsg(from, to))))
		return err
	}

	_, err = h.Commands()["createRole"].Handler(ctx, must.NotFail(documentOpMsg(must.NotFail(types.NewDocument(
		"createRole", "r", "privileges", types.MakeArray(0), "roles", types.MakeArray(0), "$db", "admin",
	)))))
	require.NoError(t, err)

	require.NoError(t, rename("x.a.b", "x.c"))
	require.NoError(t, rename("x.c", "x.d.e"))

	for _, tc := range []struct{ from, to, msg string }{
		{"admin.system.users", "admin.u2", "renaming system.users collection or renaming to system.users is not allowed"},
		{"x.plain", "admin.system.users", "renaming system.users collection or renaming to system.users is not allowed"},
		{"x.plain", "x.system.js", "renaming system.js collection or renaming to system.js is not allowed"},
		{"x.plain", "x.system.views", "error with target namespace: cannot write to x.system.views"},
		{"x.plain", "x.system.plain", "error with target namespace: Invalid system namespace: x.system.plain"},
		{"x.system.profile", "x.p2", "error with source namespace: cannot write to x.system.profile"},
		{"admin.system.roles", "admin.r2", "the admin database is reserved"},
		{"x.plain", "admin.p2", "the admin database is reserved"},
	} {
		err := rename(tc.from, tc.to)
		var commandErr *handlererrors.CommandError
		require.True(t, errors.As(err, &commandErr), "%s -> %s: %v", tc.from, tc.to, err)
		require.Equal(t, handlererrors.ErrIllegalOperation, commandErr.Code(), "%s -> %s: %v", tc.from, tc.to, err)
		require.Contains(t, commandErr.Error(), tc.msg)
	}
}
