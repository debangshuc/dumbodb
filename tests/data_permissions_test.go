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


//go:build !windows

package tests

import (
	"context"
	"io/fs"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// Like mongod with its default honorSystemUmask=false, DumboDB must create its
// files 0600 and directories 0700 whatever the umask, so other local users
// cannot read the data or the SCRAM credentials in admin.
func TestDataFilesAreOwnerOnly(t *testing.T) {
	previous := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(previous) })

	env := startDumboDB(t)
	ctx := context.Background()
	_, err := env.Client.Database("perms").Collection("c").InsertOne(ctx, bson.D{{Key: "x", Value: 1}})
	require.NoError(t, err)
	require.NoError(t, env.Client.Database("admin").RunCommand(ctx, bson.D{
		{Key: "createUser", Value: "someone"}, {Key: "pwd", Value: "pw"}, {Key: "roles", Value: bson.A{}},
	}).Err())
	require.NoError(t, env.Client.Database("perms").RunCommand(ctx, bson.D{
		{Key: "dumboCommit", Value: 1}, {Key: "message", Value: "m"},
	}).Err())

	dataDir := env.DataDir()
	checked := 0
	require.NoError(t, filepath.WalkDir(dataDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || path == dataDir {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			checked++
			require.Zero(t, info.Mode().Perm()&0o077, "%s has mode %v", path, info.Mode().Perm())
		}
		return nil
	}))
	require.NotZero(t, checked)
}
