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

package tests

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// As in MongoDB, an insert into a database whose name differs only in case
// from an existing one fails with a DatabaseDifferCase write error, not a
// command error (parity Database_NamesDifferingOnlyInCase).
func TestInsertDatabaseDifferCaseIsWriteError(t *testing.T) {
	env := startDumboDB(t)
	ctx := context.Background()

	_, err := env.Client.Database("Shop").Collection("c").InsertOne(ctx, bson.D{{Key: "a", Value: 1}})
	require.NoError(t, err)

	_, err = env.Client.Database("shop").Collection("c").InsertOne(ctx, bson.D{{Key: "a", Value: 1}})
	var writeErr mongo.WriteException
	require.True(t, errors.As(err, &writeErr), "want a write error, got %T: %v", err, err)
	require.Len(t, writeErr.WriteErrors, 1)
	require.Equal(t, 13297, writeErr.WriteErrors[0].Code)
	require.Equal(t, "db already exists with different case already have: [Shop] trying to create [shop]", writeErr.WriteErrors[0].Message)
}
