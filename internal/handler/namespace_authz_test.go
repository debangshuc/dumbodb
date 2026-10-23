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

package handler

import (
	"context"
	"testing"

	"github.com/FerretDB/wire"
	"github.com/stretchr/testify/require"

	"github.com/dolthub/dumbodb/internal/clientconn/conninfo"
	"github.com/dolthub/dumbodb/internal/handler/handlererrors"
	"github.com/dolthub/dumbodb/internal/types"
	"github.com/dolthub/dumbodb/internal/util/must"
)

func renameMsg(t *testing.T, db, from, to string, extra ...any) *wire.OpMsg {
	t.Helper()
	pairs := append([]any{"renameCollection", from, "to", to}, extra...)
	pairs = append(pairs, "$db", db)
	return must.NotFail(documentOpMsg(must.NotFail(types.NewDocument(pairs...))))
}

func dataSizeMsg(t *testing.T, db, ns string) *wire.OpMsg {
	t.Helper()
	return must.NotFail(documentOpMsg(must.NotFail(types.NewDocument("dataSize", ns, "$db", db))))
}

func authCtx(user, db string) context.Context {
	ci := conninfo.New()
	ci.SetAuth(user, "", nil, db)
	return conninfo.Ctx(context.Background(), ci)
}

// Before the fix renameCollection and dataSize were authorized against
// ($db, "<full namespace>"), while the handlers acted on the database named
// inside the namespace. A database-wide grant on x therefore matched
// "victim.orders" or "admin.system.users".
func TestAuthorize_RenameCollectionChecksNamespaces(t *testing.T) {
	h := authGateHandler(t, true)
	createUserWithRole(t, h, "x", "xowner", "dbOwner")
	createUserWithRole(t, h, "victim", "vowner", "dbOwner")
	createUserWithRole(t, h, "admin", "boss", "root")

	x := authCtx("xowner", "x")
	require.True(t, isUnauthorized(t, h.authorize(x, renameMsg(t, "x", "victim.orders", "victim.junk"))))
	require.True(t, isUnauthorized(t, h.authorize(x, renameMsg(t, "x", "admin.system.users", "admin.bak"))))
	require.True(t, isUnauthorized(t, h.authorize(x, renameMsg(t, "admin", "victim.orders", "victim.junk"))))
	require.NoError(t, h.authorize(x, renameMsg(t, "admin", "x.a", "x.b")))
	require.NoError(t, h.authorize(x, renameMsg(t, "admin", "x.a", "x.b", "dropTarget", true)))

	v := authCtx("vowner", "victim")
	require.NoError(t, h.authorize(v, renameMsg(t, "admin", "victim.orders", "victim.junk")))

	boss := authCtx("boss", "admin")
	require.True(t, isUnauthorized(t, h.authorize(boss, renameMsg(t, "admin", "admin.system.users", "admin.bak"))),
		"system collections are not covered by database-wide grants")
	require.NoError(t, h.authorize(boss, renameMsg(t, "admin", "victim.orders", "victim.junk")))
}

func TestAuthorize_DataSizeChecksNamespace(t *testing.T) {
	h := authGateHandler(t, true)
	createUserWithRole(t, h, "x", "xreader", "read")

	x := authCtx("xreader", "x")
	require.True(t, isUnauthorized(t, h.authorize(x, dataSizeMsg(t, "x", "admin.system.users"))))
	require.True(t, isUnauthorized(t, h.authorize(x, dataSizeMsg(t, "x", "victim.orders"))))
	require.NoError(t, h.authorize(x, dataSizeMsg(t, "x", "x.orders")))
}

func TestRenameCollection_RequiresAdminDatabase(t *testing.T) {
	h := authGateHandler(t, false)
	ctx := conninfo.Ctx(context.Background(), conninfo.New())

	_, err := h.MsgRenameCollection(ctx, renameMsg(t, "x", "x.a", "x.b"))
	code, ok := commandErrorCode(err)
	require.True(t, ok, "want CommandError, got %v", err)
	require.Equal(t, handlererrors.ErrUnauthorized, code)
}

func rolesInfoMsg(t *testing.T, db string, arg any) *wire.OpMsg {
	t.Helper()
	return must.NotFail(documentOpMsg(must.NotFail(types.NewDocument("rolesInfo", arg, "$db", db))))
}

func roleRef(role, db string) *types.Document {
	return must.NotFail(types.NewDocument("role", role, "db", db))
}

// Before the fix rolesInfo only checked viewRole on $db, so a user
// administrator of one database could read role definitions from any other.
func TestAuthorize_RolesInfoChecksEachRoleDatabase(t *testing.T) {
	h := authGateHandler(t, true)
	createUserWithRole(t, h, "x", "xadmin", "userAdmin")
	createUserWithRole(t, h, "x", "xreader", "read")

	xadmin := authCtx("xadmin", "x")
	xreader := authCtx("xreader", "x")

	require.NoError(t, h.authorize(xadmin, rolesInfoMsg(t, "x", int32(1))))
	require.NoError(t, h.authorize(xadmin, rolesInfoMsg(t, "x", "anyRole")))
	require.NoError(t, h.authorize(xadmin, rolesInfoMsg(t, "x", roleRef("anyRole", "x"))))

	require.True(t, isUnauthorized(t, h.authorize(xadmin, rolesInfoMsg(t, "x", roleRef("secret", "victim")))))
	require.True(t, isUnauthorized(t, h.authorize(xadmin, rolesInfoMsg(t, "x",
		must.NotFail(types.NewArray(roleRef("anyRole", "x"), roleRef("secret", "victim")))))))
	require.True(t, isUnauthorized(t, h.authorize(xadmin, rolesInfoMsg(t, "victim", int32(1)))))

	// As in MongoDB, users may always look up roles granted to them directly.
	require.NoError(t, h.authorize(xreader, rolesInfoMsg(t, "x", roleRef("read", "x"))))
	require.True(t, isUnauthorized(t, h.authorize(xreader, rolesInfoMsg(t, "x", roleRef("readWrite", "x")))))
	require.True(t, isUnauthorized(t, h.authorize(xreader, rolesInfoMsg(t, "x", int32(1)))))
}
