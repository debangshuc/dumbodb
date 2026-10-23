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
	"strings"
	"testing"

	"github.com/FerretDB/wire/wirebson"
	"github.com/stretchr/testify/require"

	"github.com/dolthub/dumbodb/internal/backends"
	"github.com/dolthub/dumbodb/internal/bson"
	"github.com/dolthub/dumbodb/internal/clientconn/conninfo"
	"github.com/dolthub/dumbodb/internal/types"
	"github.com/dolthub/dumbodb/internal/util/must"
)

// Before the fix remote URLs, including tokens embedded in them, were
// returned verbatim to every user allowed to run dumboRemote on the database.
func TestDumboRemoteRedactsURLCredentials(t *testing.T) {
	h := handlerForTest(t)
	ctx := conninfo.Ctx(context.Background(), conninfo.New())

	database, err := h.b.Database("shop")
	require.NoError(t, err)
	require.NoError(t, database.CreateCollection(ctx, &backends.CreateCollectionParams{Name: "c"}))

	run := func(pairs ...any) *types.Document {
		res, err := h.MsgDumboDBRemote(ctx, must.NotFail(documentOpMsg(must.NotFail(types.NewDocument(pairs...)))))
		require.NoError(t, err)
		doc, err := opMsgDocument(res)
		require.NoError(t, err)
		return doc
	}

	added := run("dumboRemote", int32(1), "action", "add", "name", "origin", "url", "https://user:s3cr3t@doltremoteapi.dolthub.com/org/shop", "$db", "shop")
	listed := run("dumboRemote", int32(1), "action", "list", "$db", "shop")
	for _, doc := range []*types.Document{added, listed} {
		text := wirebson.LogMessage(must.NotFail(bson.FromDocument(doc)))
		require.False(t, strings.Contains(text, "s3cr3t"), "response exposes the token: %s", text)
		require.Contains(t, text, "doltremoteapi.dolthub.com/org/shop")
	}
}

// Errors can carry a URL from DumboDB's own messages or from the storage
// layer; both reach the client and the command log.
func TestCommandErrorsRedactURLCredentials(t *testing.T) {
	h := handlerForTest(t)
	ctx := conninfo.Ctx(context.Background(), conninfo.New())

	for _, from := range []string{"ftp://user:s3cr3t@example.com/org/db", "https://user:s3cr3t@127.0.0.1:1/org/db"} {
		_, err := h.Commands()["dumboClone"].Handler(ctx, must.NotFail(documentOpMsg(must.NotFail(types.NewDocument(
			"dumboClone", int32(1), "from", from, "as", "copy", "$db", "admin",
		)))))
		require.Error(t, err, from)
		require.False(t, strings.Contains(err.Error(), "s3cr3t"), "error exposes the token: %v", err)
	}
}
