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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// $out and $merge now write as they read. $out must still replace its target
// atomically, and $merge must still match across its batches.
func TestOutAndMergeStreamCorrectly(t *testing.T) {
	env := startDumboDB(t)
	ctx := context.Background()
	db := env.Client.Database("streaming")

	const n = 2500
	docs := make([]any, n)
	for i := range docs {
		divisor := 1
		if i == n-1 {
			divisor = 0
		}
		docs[i] = bson.D{{Key: "_id", Value: i}, {Key: "v", Value: i}, {Key: "d", Value: divisor}}
	}
	_, err := db.Collection("src").InsertMany(ctx, docs)
	require.NoError(t, err)
	_, err = db.Collection("target").InsertOne(ctx, bson.D{{Key: "_id", Value: "old"}})
	require.NoError(t, err)

	collections := func() []string {
		names, err := db.ListCollectionNames(ctx, bson.D{})
		require.NoError(t, err)
		return names
	}
	requireNoTempCollections := func() {
		for _, name := range collections() {
			require.False(t, strings.HasPrefix(name, "tmp.agg_out."), "temporary collection %q left behind", name)
		}
	}

	t.Run("OutFailureLeavesTargetUnchanged", func(t *testing.T) {
		cursor, err := db.Collection("src").Aggregate(ctx, bson.A{
			bson.D{{Key: "$project", Value: bson.D{{Key: "q", Value: bson.D{{Key: "$divide", Value: bson.A{"$v", "$d"}}}}}}},
			bson.D{{Key: "$out", Value: "target"}},
		})
		if err == nil {
			err = cursor.Err()
			cursor.Close(ctx)
		}
		require.Error(t, err, "dividing by zero on the last document must fail the pipeline")

		count, err := db.Collection("target").CountDocuments(ctx, bson.D{})
		require.NoError(t, err)
		require.EqualValues(t, 1, count, "a failed $out must leave the target unchanged")
		requireNoTempCollections()
	})

	t.Run("OutReplacesTarget", func(t *testing.T) {
		_, err := db.Collection("src").Aggregate(ctx, bson.A{bson.D{{Key: "$out", Value: "target"}}})
		require.NoError(t, err)
		count, err := db.Collection("target").CountDocuments(ctx, bson.D{})
		require.NoError(t, err)
		require.EqualValues(t, n, count)
		require.ErrorIs(t, db.Collection("target").FindOne(ctx, bson.D{{Key: "_id", Value: "old"}}).Err(), mongo.ErrNoDocuments)
		requireNoTempCollections()
	})

	t.Run("MergeMatchesAcrossBatches", func(t *testing.T) {
		_, err := db.Collection("src").Aggregate(ctx, bson.A{
			bson.D{{Key: "$project", Value: bson.D{{Key: "w", Value: "$v"}}}},
			bson.D{{Key: "$merge", Value: bson.D{{Key: "into", Value: "target"}, {Key: "whenMatched", Value: "merge"}}}},
		})
		require.NoError(t, err)
		count, err := db.Collection("target").CountDocuments(ctx, bson.D{{Key: "w", Value: bson.D{{Key: "$exists", Value: true}}}, {Key: "v", Value: bson.D{{Key: "$exists", Value: true}}}})
		require.NoError(t, err)
		require.EqualValues(t, n, count, "every target document must be merged, whichever batch it was in")
	})
}

// As in MongoDB, $out and $merge give documents without an _id a generated
// ObjectId (found by parity AggOut_FailureLeavesTargetUnchanged).
func TestOutAndMergeGenerateMissingIDs(t *testing.T) {
	env := startDumboDB(t)
	ctx := context.Background()
	db := env.Client.Database("ids")
	_, err := db.Collection("src").InsertMany(ctx, []any{
		bson.D{{Key: "_id", Value: 1}, {Key: "sub", Value: bson.D{{Key: "n", Value: 1}}}},
		bson.D{{Key: "_id", Value: 2}, {Key: "sub", Value: bson.D{{Key: "n", Value: 2}}}},
	})
	require.NoError(t, err)
	noID := bson.D{{Key: "$replaceRoot", Value: bson.D{{Key: "newRoot", Value: "$sub"}}}}

	for name, last := range map[string]bson.D{
		"$out":   {{Key: "$out", Value: "out_target"}},
		"$merge": {{Key: "$merge", Value: "merge_target"}},
	} {
		cursor, err := db.Collection("src").Aggregate(ctx, bson.A{noID, last})
		require.NoError(t, err, name)
		require.NoError(t, cursor.All(ctx, &[]bson.M{}), name)
	}
	for _, target := range []string{"out_target", "merge_target"} {
		var docs []bson.M
		cursor, err := db.Collection(target).Find(ctx, bson.D{})
		require.NoError(t, err)
		require.NoError(t, cursor.All(ctx, &docs))
		require.Len(t, docs, 2, target)
		for _, doc := range docs {
			require.IsType(t, bson.ObjectID{}, doc["_id"], target)
		}
	}
}
