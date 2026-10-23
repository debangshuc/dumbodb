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
	"github.com/dolthub/dumbodb/internal/types"
	"github.com/dolthub/dumbodb/internal/util/must"
)

type versionedBackend interface {
	backends.Backend
	backends.VersioningBackend
}

type pullRecordingBackend struct {
	versionedBackend
	pulled *backends.PullParams
}

func (b *pullRecordingBackend) DumboDBPull(_ context.Context, params *backends.PullParams) (*backends.PullResult, error) {
	b.pulled = params
	return nil, errors.New("recorded")
}

// Before the fix dumboPull passed the client's author straight through, so
// under --auth its merge commits carried the default identity, or one the
// client chose, instead of the caller's, unlike commit, merge and revert.
func TestDumboPullStampsCallerIdentity(t *testing.T) {
	h := authGateHandler(t, true)
	createUserWithRole(t, h, "admin", "alice", "root")
	recorder := &pullRecordingBackend{versionedBackend: h.b.(versionedBackend)}
	h.b = recorder

	ctx := authCtx("alice", "admin")
	want, ok, err := h.commitIdentityString(ctx)
	require.NoError(t, err)
	require.True(t, ok)

	_, pullErr := h.MsgDumboDBPull(ctx, must.NotFail(documentOpMsg(must.NotFail(types.NewDocument(
		"dumboPull", int32(1), "from", "origin", "$db", "shop",
	)))))
	require.NotNil(t, recorder.pulled, "pull did not reach the backend: %v", pullErr)
	require.Equal(t, want, recorder.pulled.Author)
	require.Equal(t, want, recorder.pulled.Committer)
}
