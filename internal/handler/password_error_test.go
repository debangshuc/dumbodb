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
	"strings"
	"testing"

	"github.com/FerretDB/wire"
	"github.com/stretchr/testify/require"

	"github.com/dolthub/dumbodb/internal/clientconn/conninfo"
	"github.com/dolthub/dumbodb/internal/handler/handlererrors"
	"github.com/dolthub/dumbodb/internal/types"
	"github.com/dolthub/dumbodb/internal/util/must"
)

// Before the fix a password failing SASLprep's bidi rule was formatted into
// the error returned to the client and logged by the command wrapper.
func TestSASLprepFailureDoesNotEchoPassword(t *testing.T) {
	h := authGateHandler(t, false)
	ctx := conninfo.Ctx(context.Background(), conninfo.New())
	const bidiPassword = "plaintextsecret\u05d0"

	createUser := func(user, pwd string) *wire.OpMsg {
		return must.NotFail(documentOpMsg(must.NotFail(types.NewDocument(
			"createUser", user, "pwd", pwd, "roles", types.MakeArray(0), "$db", "x",
		))))
	}
	updateUser := must.NotFail(documentOpMsg(must.NotFail(types.NewDocument(
		"updateUser", "existing", "pwd", bidiPassword, "$db", "x",
	))))
	_, err := h.MsgCreateUser(ctx, createUser("existing", "goodpassword"))
	require.NoError(t, err)

	for name, call := range map[string]func() error{
		"createUser": func() error { _, err := h.MsgCreateUser(ctx, createUser("fresh", bidiPassword)); return err },
		"updateUser": func() error { _, err := h.MsgUpdateUser(ctx, updateUser); return err },
	} {
		err := call()
		require.Error(t, err, name)
		require.False(t, strings.Contains(err.Error(), "plaintextsecret"), "%s error contains the password: %v", name, err)
		var commandErr *handlererrors.CommandError
		require.True(t, errors.As(err, &commandErr), "%s: %v", name, err)
		require.Equal(t, handlererrors.ErrStringProhibited, commandErr.Code(), name)
		require.Contains(t, commandErr.Error(), "U_STRINGPREP_CHECK_BIDI_ERROR", name)
	}
}

// A role reference to a database named "a.b" would resolve to the role "b.c"
// of database "a" through the shared "<db>.<role>" _id; MongoDB rejects it.
func TestRoleReferencesRejectInvalidDatabaseNames(t *testing.T) {
	h := authGateHandler(t, false)
	ctx := conninfo.Ctx(context.Background(), conninfo.New())

	_, err := h.MsgCreateUser(ctx, must.NotFail(documentOpMsg(must.NotFail(types.NewDocument(
		"createUser", "u", "pwd", "pw",
		"roles", must.NotFail(types.NewArray(must.NotFail(types.NewDocument("role", "c", "db", "a.b")))),
		"$db", "x",
	)))))
	var commandErr *handlererrors.CommandError
	require.True(t, errors.As(err, &commandErr), "%v", err)
	require.Equal(t, handlererrors.ErrInvalidNamespace, commandErr.Code())
	require.Contains(t, commandErr.Error(), "'.' is an invalid character in a db name: a.b")
}

// Users and roles live in plain databases; a ref suffix would let "a@b.c" +
// "d" and "a@b" + "c.d" share the _id "a@b.c.d".
func TestPrincipalCommandsRequirePlainDatabaseName(t *testing.T) {
	h := authGateHandler(t, false)
	ctx := conninfo.Ctx(context.Background(), conninfo.New())

	for _, cmd := range []*wire.OpMsg{
		must.NotFail(documentOpMsg(must.NotFail(types.NewDocument("createUser", "d", "pwd", "pw", "roles", types.MakeArray(0), "$db", "a@b.c")))),
		must.NotFail(documentOpMsg(must.NotFail(types.NewDocument("createRole", "d", "privileges", types.MakeArray(0), "roles", types.MakeArray(0), "$db", "a@b.c")))),
		must.NotFail(documentOpMsg(must.NotFail(types.NewDocument("usersInfo", must.NotFail(types.NewDocument("user", "c", "db", "a.b")), "$db", "x")))),
		must.NotFail(documentOpMsg(must.NotFail(types.NewDocument("rolesInfo", must.NotFail(types.NewDocument("role", "c", "db", "a.b")), "$db", "x")))),
	} {
		name := wireCommandName(cmd)
		_, err := h.commands[name].Handler(ctx, cmd)
		var commandErr *handlererrors.CommandError
		require.True(t, errors.As(err, &commandErr), "%s: %v", name, err)
		require.Equal(t, handlererrors.ErrInvalidNamespace, commandErr.Code(), "%s: %v", name, err)
	}
}
