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

package stages

import (
	"context"
	"fmt"

	"github.com/dolthub/dumbodb/internal/backends"
	"github.com/dolthub/dumbodb/internal/handler/common/aggregations"
	"github.com/dolthub/dumbodb/internal/handler/handlererrors"
	"github.com/dolthub/dumbodb/internal/types"
	"github.com/dolthub/dumbodb/internal/util/iterator"
	"github.com/dolthub/dumbodb/internal/util/must"
)

// planCacheStatsStage represents $planCacheStats, which lists the
// collection's cached query plans.
type planCacheStatsStage struct {
	docs []*types.Document
}

// NewPlanCacheStatsStage reads c's plan cache. ns names c in the error for a
// missing collection.
func NewPlanCacheStatsStage(ctx context.Context, c backends.Collection, ns, host string) (aggregations.Stage, error) {
	pc, ok := c.(backends.PlanCacheCollection)
	if !ok {
		return &planCacheStatsStage{}, nil
	}
	entries, err := pc.PlanCacheStats(ctx)
	if backends.ErrorCodeIs(err, backends.ErrorCodeCollectionDoesNotExist) {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrAggregateCollectionMissing,
			fmt.Sprintf("PlanExecutor error during aggregation :: caused by :: collection '%s' does not exist", ns),
			"$planCacheStats",
		)
	}
	if err != nil {
		return nil, err
	}

	docs := make([]*types.Document, len(entries))
	for i, e := range entries {
		filter, sort := e.Filter, e.Sort
		if filter == nil {
			filter = must.NotFail(types.NewDocument())
		}
		if sort == nil {
			sort = must.NotFail(types.NewDocument())
		}
		docs[i] = must.NotFail(types.NewDocument(
			"version", "1",
			"queryHash", e.ShapeHash,
			"planCacheShapeHash", e.ShapeHash,
			"planCacheKey", e.Key,
			"isActive", true,
			"works", e.Works,
			"worksType", "works",
			"timeOfCreation", e.Created,
			"createdFromQuery", must.NotFail(types.NewDocument(
				"query", filter,
				"sort", sort,
				"projection", must.NotFail(types.NewDocument()),
			)),
			"cachedPlan", e.CachedPlan,
			"creationExecStats", types.MakeArray(0),
			"candidatePlanScores", types.MakeArray(0),
			"indexFilterSet", false,
			"estimatedSizeBytes", int64(0),
			"host", host,
		))
	}
	return &planCacheStatsStage{docs: docs}, nil
}

// Process implements aggregations.Stage.
// It discards the input iterator and returns one document per cached plan.
func (s *planCacheStatsStage) Process(ctx context.Context, iter types.DocumentsIterator, closer *iterator.MultiCloser) (types.DocumentsIterator, error) {
	iter.Close()

	result := iterator.Values(iterator.ForSlice(s.docs))
	closer.Add(result)

	return result, nil
}

var _ aggregations.Stage = (*planCacheStatsStage)(nil)
