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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dolthub/dumbodb/internal/backends"
)

func logCommits(t *testing.T, b *Backend, dbName, branch string) []backends.CommitInfo {
	t.Helper()
	res, err := b.DumboDBLog(context.Background(), &backends.LogParams{DBName: dbName, Branch: branch, ConnBranch: branch})
	require.NoError(t, err)
	return res.Commits
}

func amend(t *testing.T, b *Backend, dbName, branch, message string) *backends.CommitResult {
	t.Helper()
	res, err := b.DumboDBCommit(context.Background(), &backends.CommitParams{
		DBName: dbName, Branch: branch, Message: message, Author: "testuser", Amend: true,
	})
	require.NoError(t, err)
	return res
}

func TestDumboDBCommitAmendMain(t *testing.T) {
	b := newTestBackend(t)
	insertDoc(t, b, "testdb", "coll", mustDoc(t, "_id", int64(1)))
	first := commitDB(t, b, "testdb", "first")
	insertDoc(t, b, "testdb", "coll", mustDoc(t, "_id", int64(2)))
	second := commitDB(t, b, "testdb", "second")

	insertDoc(t, b, "testdb", "coll", mustDoc(t, "_id", int64(3)))
	amended := amend(t, b, "testdb", "main", "")
	assert.NotEqual(t, second, amended.CommitID)
	assert.Equal(t, "second", amended.Message, "an amend without a message keeps HEAD's message")

	commits := logCommits(t, b, "testdb", "main")
	require.Len(t, commits, 3, "the amended commit replaces HEAD rather than adding to it")
	assert.Equal(t, amended.CommitID, commits[0].CommitID)
	assert.Equal(t, first, commits[0].Parent1)
	assert.Equal(t, "second", commits[0].Message)

	reworded := amend(t, b, "testdb", "main", "reworded")
	commits = logCommits(t, b, "testdb", "main")
	require.Len(t, commits, 3, "an amend with no working changes is allowed, as in Dolt")
	assert.Equal(t, reworded.CommitID, commits[0].CommitID)
	assert.Equal(t, first, commits[0].Parent1)
	assert.Equal(t, "reworded", commits[0].Message)

	_, err := b.DumboDBCommit(context.Background(), &backends.CommitParams{DBName: "testdb", Message: "noop", Author: "testuser"})
	assert.ErrorIs(t, err, backends.ErrEmptyCommit, "the amended commit holds the working changes")
}

func TestDumboDBCommitAmendBranch(t *testing.T) {
	b := newTestBackend(t)
	insertDoc(t, b, "testdb", "coll", mustDoc(t, "_id", int64(1)))
	base := commitDB(t, b, "testdb", "base")
	_, err := b.DumboDBBranch(context.Background(), &backends.BranchParams{DBName: "testdb", Action: "add", From: "main", Name: "feature"})
	require.NoError(t, err)

	insertDoc(t, b, "testdb@feature", "coll", mustDoc(t, "_id", int64(2)))
	featureCommit, err := b.DumboDBCommit(context.Background(), &backends.CommitParams{
		DBName: "testdb", Branch: "feature", Message: "feature work", Author: "testuser",
	})
	require.NoError(t, err)

	insertDoc(t, b, "testdb@feature", "coll", mustDoc(t, "_id", int64(3)))
	amended := amend(t, b, "testdb", "feature", "feature work, amended")
	assert.NotEqual(t, featureCommit.CommitID, amended.CommitID)

	commits := logCommits(t, b, "testdb", "feature")
	require.Len(t, commits, 3)
	assert.Equal(t, amended.CommitID, commits[0].CommitID)
	assert.Equal(t, base, commits[0].Parent1)
	assert.Equal(t, "feature work, amended", commits[0].Message)

	_, err = b.DumboDBCommit(context.Background(), &backends.CommitParams{DBName: "testdb", Branch: "feature", Message: "noop", Author: "testuser"})
	assert.ErrorIs(t, err, backends.ErrEmptyCommit, "the amended commit holds the working changes")

	mainCommits := logCommits(t, b, "testdb", "main")
	assert.Equal(t, base, mainCommits[0].CommitID, "amending feature leaves main alone")
}

func TestDumboDBCommitAmendRootCommit(t *testing.T) {
	b := newTestBackend(t)
	insertDoc(t, b, "testdb", "coll", mustDoc(t, "_id", int64(1)))
	require.Len(t, logCommits(t, b, "testdb", "main"), 1)

	amended := amend(t, b, "testdb", "main", "")
	commits := logCommits(t, b, "testdb", "main")
	require.Len(t, commits, 1)
	assert.Equal(t, amended.CommitID, commits[0].CommitID)
	assert.Empty(t, commits[0].Parent1)
	assert.Equal(t, "Initialize database", commits[0].Message)
}
