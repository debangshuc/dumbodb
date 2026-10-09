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
	"slices"
	"testing"

	"github.com/dolthub/dumbodb/internal/backends"
	"github.com/dolthub/dumbodb/internal/handler/common/aggregations"
	"github.com/dolthub/dumbodb/internal/handler/common/aggregations/stages"
	"github.com/dolthub/dumbodb/internal/types"
	"github.com/dolthub/dumbodb/internal/util/iterator"
	"github.com/dolthub/dumbodb/internal/util/must"
)

func TestPushdownLimit(t *testing.T) {
	t.Parallel()

	match := doc("$match", doc("p", "x"))
	sort := doc("$sort", doc("k", int32(1)))
	limit := doc("$limit", int32(5))
	for name, tc := range map[string]struct {
		stages []any
		want   int64
	}{
		"sort limit":           {[]any{sort, limit}, 5},
		"match sort limit":     {[]any{match, sort, limit}, 5},
		"sort match limit":     {[]any{sort, match, limit}, 5},
		"match limit":          {[]any{match, limit}, 5},
		"limit first":          {[]any{limit}, 0},
		"no limit":             {[]any{sort, match}, 0},
		"project before limit": {[]any{sort, doc("$project", doc("k", int32(1))), limit}, 0},
		"two matches":          {[]any{match, match, limit}, 0},
		"skip before limit":    {[]any{sort, doc("$skip", int32(1)), limit}, 0},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := pushdownLimit(tc.stages); got != tc.want {
				t.Fatalf("pushdownLimit = %d, want %d", got, tc.want)
			}
		})
	}
}

type sortedQueryStub struct {
	backends.Collection
	docs   []*types.Document
	sorted bool
}

func (s *sortedQueryStub) Query(context.Context, *backends.QueryParams) (*backends.QueryResult, error) {
	return &backends.QueryResult{Iter: iterator.Values(iterator.ForSlice(s.docs)), Sorted: s.sorted}, nil
}

// A $sort stage the backend already satisfied is skipped, so documents keep
// the backend's order (here deliberately unsorted to make the skip visible).
func TestProcessStagesDocumentsSkipsBackendSortedStage(t *testing.T) {
	t.Parallel()

	for _, sorted := range []bool{true, false} {
		sortStage := must.NotFail(stages.NewSortStage(doc("$sort", doc("k", int32(1))), 0, nil))
		stub := &sortedQueryStub{
			docs:   []*types.Document{doc("k", int32(2)), doc("k", int32(1))},
			sorted: sorted,
		}
		closer := iterator.NewMultiCloser()
		iter, err := processStagesDocuments(context.Background(), closer, &stagesDocumentsParams{
			c: stub, qp: new(backends.QueryParams), stages: []aggregations.Stage{sortStage}, sortStage: sortStage,
		})
		if err != nil {
			t.Fatal(err)
		}
		docs := must.NotFail(iterator.ConsumeValues(iter))
		closer.Close()
		var ks []int32
		for _, d := range docs {
			ks = append(ks, must.NotFail(d.Get("k")).(int32))
		}
		want := []int32{1, 2}
		if sorted {
			want = []int32{2, 1}
		}
		if !slices.Equal(ks, want) {
			t.Fatalf("backend sorted=%v: got %v, want %v", sorted, ks, want)
		}
	}
}
