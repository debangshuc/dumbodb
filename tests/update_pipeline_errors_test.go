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

// As in MongoDB, an expression error while applying a pipeline-style update
// is a write error of the update command (parity
// Arithmetic_Divide_InUpdatePipeline).
func TestUpdatePipelineExpressionErrorIsWriteError(t *testing.T) {
	env := startDumboDB(t)
	ctx := context.Background()
	col := env.Client.Database("updpipe").Collection("c")
	_, err := col.InsertOne(ctx, bson.D{{Key: "_id", Value: 1}, {Key: "v", Value: 7}})
	require.NoError(t, err)

	divideByZero := bson.A{bson.D{{Key: "$set", Value: bson.D{{Key: "r", Value: bson.D{{Key: "$divide", Value: bson.A{"$v", 0}}}}}}}}

	_, err = col.UpdateOne(ctx, bson.D{{Key: "_id", Value: 1}}, divideByZero)
	var writeErr mongo.WriteException
	require.True(t, errors.As(err, &writeErr), "want a write error, got %T: %v", err, err)
	require.Len(t, writeErr.WriteErrors, 1)
	require.Equal(t, 2, writeErr.WriteErrors[0].Code)
}
