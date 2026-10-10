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
	"slices"
	"testing"

	"github.com/dolthub/dumbodb/internal/backends"
	"github.com/dolthub/dumbodb/internal/types"
	"github.com/dolthub/dumbodb/internal/util/must"
)

func sortWalkQuery(t *testing.T, coll backends.Collection, params *backends.QueryParams) ([]int32, bool) {
	t.Helper()
	res, err := coll.Query(context.Background(), params)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	defer res.Iter.Close()
	var ids []int32
	for {
		_, doc, err := res.Iter.Next()
		if err != nil {
			break
		}
		ids = append(ids, must.NotFail(doc.Get("_id")).(int32))
	}
	return ids, res.Sorted
}

func sortOn(field string, dir int64) *types.Document {
	return must.NotFail(types.NewDocument(field, dir))
}

func newSortWalkCollection(t *testing.T, idx backends.IndexInfo, docs ...*types.Document) backends.Collection {
	t.Helper()
	c := newDottedPathTestCollection(t, idx)
	for _, d := range docs {
		if err := insertDottedPathDoc(c, d); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func kDoc(id int32, k any) *types.Document {
	return must.NotFail(types.NewDocument("_id", id, "k", k))
}

func TestSortWalkOrdersByIndex(t *testing.T) {
	c := newSortWalkCollection(t, backends.IndexInfo{Name: "k", Key: []backends.IndexKeyPair{{Field: "k"}}},
		kDoc(1, int32(30)), kDoc(2, int32(10)), kDoc(3, int32(20)),
		kDoc(4, must.NotFail(types.NewDocument("x", int32(2)))),
		kDoc(5, must.NotFail(types.NewDocument("x", int32(1)))),
	)
	ids, sorted := sortWalkQuery(t, c, &backends.QueryParams{Sort: sortOn("k", 1)})
	if !sorted {
		t.Fatal("eligible single-field sort must be served by the index walk")
	}
	// The seed doc {_id: 0} lacks k and sorts as null, first.
	if want := []int32{0, 2, 3, 1, 5, 4}; !slices.Equal(ids, want) {
		t.Fatalf("ascending walk = %v, want %v", ids, want)
	}
	ids, _ = sortWalkQuery(t, c, &backends.QueryParams{Sort: sortOn("k", -1)})
	if want := []int32{4, 5, 1, 3, 2, 0}; !slices.Equal(ids, want) {
		t.Fatalf("descending walk = %v, want %v", ids, want)
	}
}

func TestSortWalkMultikeyYieldsEachDocOnceAtItsExtreme(t *testing.T) {
	arr := func(v ...any) *types.Array { return must.NotFail(types.NewArray(v...)) }
	c := newSortWalkCollection(t, backends.IndexInfo{Name: "k", Key: []backends.IndexKeyPair{{Field: "k"}}},
		kDoc(1, arr(int32(5), int32(1))), kDoc(2, int32(3)), kDoc(3, arr(int32(4), int32(9))),
	)
	ids, sorted := sortWalkQuery(t, c, &backends.QueryParams{Sort: sortOn("k", 1)})
	if !sorted {
		t.Fatal("multikey index without array keys must be walked")
	}
	if want := []int32{0, 1, 2, 3}; !slices.Equal(ids, want) {
		t.Fatalf("ascending multikey walk = %v, want %v (by minimum element)", ids, want)
	}
	ids, _ = sortWalkQuery(t, c, &backends.QueryParams{Sort: sortOn("k", -1)})
	if want := []int32{3, 1, 2, 0}; !slices.Equal(ids, want) {
		t.Fatalf("descending multikey walk = %v, want %v (by maximum element)", ids, want)
	}
}

func TestSortWalkMergesArrayKeys(t *testing.T) {
	arr := func(v ...any) *types.Array { return must.NotFail(types.NewArray(v...)) }
	c := newSortWalkCollection(t, backends.IndexInfo{Name: "k", Key: []backends.IndexKeyPair{{Field: "k"}}},
		kDoc(1, int32(5)), kDoc(2, int32(7)),
		kDoc(10, arr()),
		kDoc(11, arr(arr(int32(1), int32(2)), int32(500))),
		kDoc(12, arr(arr(int32(3)))),
		kDoc(13, arr(int32(-5), arr())),
	)
	ids, sorted := sortWalkQuery(t, c, &backends.QueryParams{Sort: sortOn("k", 1)})
	if !sorted {
		t.Fatal("multikey index holding array keys must be walked")
	}
	if want := []int32{10, 0, 13, 1, 2, 11, 12}; !slices.Equal(ids, want) {
		t.Fatalf("ascending walk = %v, want %v (empty array lowest, nested arrays by array order)", ids, want)
	}
	ids, _ = sortWalkQuery(t, c, &backends.QueryParams{Sort: sortOn("k", -1)})
	if want := []int32{12, 11, 13, 2, 1, 0, 10}; !slices.Equal(ids, want) {
		t.Fatalf("descending walk = %v, want %v", ids, want)
	}
}

func TestSortWalkDeclines(t *testing.T) {
	k := []backends.IndexKeyPair{{Field: "k"}}
	for _, tc := range []struct {
		name   string
		idx    backends.IndexInfo
		docs   []*types.Document
		params *backends.QueryParams
	}{
		{"sparse index", backends.IndexInfo{Name: "k", Key: k, Sparse: true}, nil, &backends.QueryParams{Sort: sortOn("k", 1)}},
		{"hidden index", backends.IndexInfo{Name: "k", Key: k, Hidden: true}, nil, &backends.QueryParams{Sort: sortOn("k", 1)}},
		{"hint names another index", backends.IndexInfo{Name: "k", Key: k}, nil, &backends.QueryParams{Sort: sortOn("k", 1), Hint: "_id_"}},
		{"natural hint", backends.IndexInfo{Name: "k", Key: k}, nil, &backends.QueryParams{Sort: sortOn("k", 1), Hint: must.NotFail(types.NewDocument("$natural", int32(1)))}},
		{"sort on unindexed field", backends.IndexInfo{Name: "k", Key: k}, nil, &backends.QueryParams{Sort: sortOn("other", 1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newSortWalkCollection(t, tc.idx, tc.docs...)
			if _, sorted := sortWalkQuery(t, c, tc.params); sorted {
				t.Fatal("query must not report a sorted index walk")
			}
		})
	}
}

// newSortVsFilterCollection holds docs 1..n with k = (i*7) mod n, p = i mod 3
// and q = i, indexed on each field.
func newSortVsFilterCollection(t *testing.T, n int32) backends.Collection {
	t.Helper()
	c := newSortWalkCollection(t, backends.IndexInfo{Name: "k", Key: []backends.IndexKeyPair{{Field: "k"}}})
	var docs []*types.Document
	for i := int32(1); i <= n; i++ {
		docs = append(docs, must.NotFail(types.NewDocument("_id", i, "k", (i*7)%n, "p", i%3, "q", i)))
	}
	if _, err := c.InsertAll(context.Background(), &backends.InsertAllParams{Docs: docs}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateIndexes(context.Background(), &backends.CreateIndexesParams{Indexes: []backends.IndexInfo{
		{Name: "p", Key: []backends.IndexKeyPair{{Field: "p"}}},
		{Name: "q", Key: []backends.IndexKeyPair{{Field: "q"}}},
	}}); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSortWalkTrialAgainstFilterIndex(t *testing.T) {
	c := newSortVsFilterCollection(t, 3000)
	for _, tc := range []struct {
		name       string
		filter     *types.Document
		limit      int64
		hint       any
		wantWalked bool
	}{
		{"unselective filter with limit walks", must.NotFail(types.NewDocument("p", int32(1))), 5, nil, true},
		{"unselective filter without limit walks", must.NotFail(types.NewDocument("p", int32(1))), 0, nil, true},
		{"selective filter uses its index", must.NotFail(types.NewDocument("q", int32(7))), 5, nil, false},
		{"moderate filter without limit uses its index", must.NotFail(types.NewDocument("q", must.NotFail(types.NewDocument("$lt", int32(300))))), 0, nil, false},
		{"empty filter range uses its index", must.NotFail(types.NewDocument("q", int32(-1))), 5, nil, false},
		{"hint on the filter index skips the trial", must.NotFail(types.NewDocument("p", int32(1))), 5, "p", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ids, sorted := sortWalkQuery(t, c, &backends.QueryParams{Filter: tc.filter, Sort: sortOn("k", 1), LimitHint: tc.limit, Hint: tc.hint})
			if sorted != tc.wantWalked {
				t.Fatalf("walked = %v, want %v", sorted, tc.wantWalked)
			}
			if !sorted {
				return
			}
			// The walk returns every document in k order, the trial's
			// examined documents first; the caller filters.
			if len(ids) != 3001 {
				t.Fatalf("walk returned %d documents, want 3001", len(ids))
			}
			for i := 1; i < len(ids); i++ {
				if prev, cur := (ids[i-1]*7)%3000, (ids[i]*7)%3000; ids[i-1] != 0 && prev > cur {
					t.Fatalf("walk out of k order at %d: %v", i, ids[:i+1])
				}
			}
		})
	}
}

func TestIndexScanInSortOrder(t *testing.T) {
	doc := func(id, grp int32, k any) *types.Document {
		return must.NotFail(types.NewDocument("_id", id, "grp", grp, "k", k))
	}
	sub := func(x int32) *types.Document { return must.NotFail(types.NewDocument("x", x)) }
	c := newSortWalkCollection(t, backends.IndexInfo{Name: "grp_k", Key: []backends.IndexKeyPair{{Field: "grp"}, {Field: "k", Descending: true}}},
		doc(1, 1, int32(30)), doc(2, 1, int32(10)), doc(3, 2, int32(5)), doc(4, 1, int32(20)),
		doc(5, 1, sub(2)), doc(6, 1, sub(1)), doc(7, 2, sub(0)),
	)
	insertFiller(t, c, 20)
	grp1 := must.NotFail(types.NewDocument("grp", int32(1)))
	ids, sorted := sortWalkQuery(t, c, &backends.QueryParams{Filter: grp1, Sort: sortOn("k", 1)})
	if !sorted {
		t.Fatal("an equality prefix scan yields the next key in order")
	}
	if want := []int32{2, 4, 1, 6, 5}; !slices.Equal(ids, want) {
		t.Fatalf("ascending = %v, want %v (subdocuments ordered by value)", ids, want)
	}
	ids, _ = sortWalkQuery(t, c, &backends.QueryParams{Filter: grp1, Sort: sortOn("k", -1)})
	if want := []int32{5, 6, 1, 4, 2}; !slices.Equal(ids, want) {
		t.Fatalf("descending = %v, want %v", ids, want)
	}

	in := must.NotFail(types.NewDocument("grp", must.NotFail(types.NewDocument("$in", must.NotFail(types.NewArray(int32(1), int32(2)))))))
	if _, sorted := sortWalkQuery(t, c, &backends.QueryParams{Filter: in, Sort: sortOn("k", 1)}); sorted {
		t.Fatal("$in on the prefix spans several prefixes; the scan is not in k order")
	}
}

func TestIndexScanInSortOrderOnSortField(t *testing.T) {
	c := newSortWalkCollection(t, backends.IndexInfo{Name: "k", Key: []backends.IndexKeyPair{{Field: "k"}}},
		kDoc(1, int32(30)), kDoc(2, int32(10)), kDoc(3, int32(20)), kDoc(4, int32(40)),
	)
	insertFiller(t, c, 20)
	in := must.NotFail(types.NewDocument("k", must.NotFail(types.NewDocument("$in", must.NotFail(types.NewArray(int32(40), int32(10), int32(30)))))))
	ids, sorted := sortWalkQuery(t, c, &backends.QueryParams{Filter: in, Sort: sortOn("k", 1)})
	if !sorted {
		t.Fatal("$in points scanned in key order yield sort order")
	}
	if want := []int32{2, 1, 4}; !slices.Equal(ids, want) {
		t.Fatalf("$in ascending = %v, want %v", ids, want)
	}

	arr := func(v ...any) *types.Array { return must.NotFail(types.NewArray(v...)) }
	if err := insertDottedPathDoc(c, kDoc(5, arr(int32(15), int32(50)))); err != nil {
		t.Fatal(err)
	}
	gt := must.NotFail(types.NewDocument("k", must.NotFail(types.NewDocument("$gt", int32(12)))))
	if _, sorted := sortWalkQuery(t, c, &backends.QueryParams{Filter: gt, Sort: sortOn("k", 1)}); sorted {
		t.Fatal("a multikey range scan is not in sort order")
	}
}

// insertFiller adds n documents lacking the indexed fields, so test filters
// stay under the planner's half-the-collection cap.
func insertFiller(t *testing.T, c backends.Collection, n int32) {
	t.Helper()
	for i := int32(0); i < n; i++ {
		if err := insertDottedPathDoc(c, must.NotFail(types.NewDocument("_id", 1000+i))); err != nil {
			t.Fatal(err)
		}
	}
}

func TestIndexKeysSortedBeforeFetch(t *testing.T) {
	doc := func(id, grp int32, k any) *types.Document {
		return must.NotFail(types.NewDocument("_id", id, "grp", grp, "k", k))
	}
	sub := func(x int32) *types.Document { return must.NotFail(types.NewDocument("x", x)) }
	c := newSortWalkCollection(t, backends.IndexInfo{Name: "grp_k", Key: []backends.IndexKeyPair{{Field: "grp"}, {Field: "k"}}},
		doc(1, 2, int32(30)), doc(2, 3, 2.5), doc(3, 2, int32(-4)), doc(4, 3, "s"), doc(5, 2, sub(2)),
		doc(6, 3, sub(1)), doc(7, 1, int32(0)), doc(8, 3, 2.25), doc(9, 2, int32(3)),
	)
	insertFiller(t, c, 20)
	gte2 := must.NotFail(types.NewDocument("grp", must.NotFail(types.NewDocument("$gte", int32(2)))))

	ids, sorted := sortWalkQuery(t, c, &backends.QueryParams{Filter: gte2, Sort: sortOn("k", 1)})
	if !sorted {
		t.Fatal("a range prefix scan is sorted on its keys")
	}
	if want := []int32{3, 8, 2, 9, 1, 4, 6, 5}; !slices.Equal(ids, want) {
		t.Fatalf("ascending = %v, want %v", ids, want)
	}
	ids, _ = sortWalkQuery(t, c, &backends.QueryParams{Filter: gte2, Sort: sortOn("k", -1)})
	if want := []int32{5, 6, 4, 1, 9, 2, 8, 3}; !slices.Equal(ids, want) {
		t.Fatalf("descending = %v, want %v", ids, want)
	}

	res, err := c.Query(context.Background(), &backends.QueryParams{Filter: gte2, Sort: sortOn("k", 1)})
	if err != nil {
		t.Fatal(err)
	}
	docs := queryExecutorCounters.docs.Load()
	for range 2 {
		if _, _, err := res.Iter.Next(); err != nil {
			t.Fatal(err)
		}
	}
	res.Iter.Close()
	if read := queryExecutorCounters.docs.Load() - docs; read != 2 {
		t.Fatalf("reading 2 sorted documents fetched %d", read)
	}

	residual := must.NotFail(types.NewDocument("grp", must.NotFail(types.NewDocument("$gte", int32(2))), "other", int32(1)))
	if _, sorted := sortWalkQuery(t, c, &backends.QueryParams{Filter: residual, Sort: sortOn("k", 1)}); sorted {
		t.Fatal("a filter field outside the index is decided after the fetch; the keys cannot be sorted first")
	}
}
