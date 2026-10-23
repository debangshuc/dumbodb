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
	"testing"

	"github.com/dolthub/dumbodb/internal/backends"
	"github.com/dolthub/dumbodb/internal/types"
	"github.com/dolthub/dumbodb/internal/util/must"
)

func TestQueryShape(t *testing.T) {
	doc := func(pairs ...any) *types.Document { return must.NotFail(types.NewDocument(pairs...)) }
	arr := func(v ...any) *types.Array { return must.NotFail(types.NewArray(v...)) }
	shape := func(p backends.QueryParams) string { return queryShape(&p) }
	base := backends.QueryParams{Filter: doc("grp", int32(7)), Sort: doc("k", int64(1)), LimitHint: 10}

	same := []struct {
		name string
		p    backends.QueryParams
	}{
		{"another value", backends.QueryParams{Filter: doc("grp", int32(8)), Sort: doc("k", int64(1)), LimitHint: 10}},
		{"another number type", backends.QueryParams{Filter: doc("grp", 7.5), Sort: doc("k", int64(1)), LimitHint: 10}},
	}
	for _, tc := range same {
		if shape(tc.p) != shape(base) {
			t.Errorf("%s: shape %q, want %q", tc.name, shape(tc.p), shape(base))
		}
	}

	differ := []struct {
		name string
		p    backends.QueryParams
	}{
		{"another field", backends.QueryParams{Filter: doc("x", int32(7)), Sort: doc("k", int64(1)), LimitHint: 10}},
		{"another value class", backends.QueryParams{Filter: doc("grp", "7"), Sort: doc("k", int64(1)), LimitHint: 10}},
		{"an operator", backends.QueryParams{Filter: doc("grp", doc("$lt", int32(7))), Sort: doc("k", int64(1)), LimitHint: 10}},
		{"another sort direction", backends.QueryParams{Filter: doc("grp", int32(7)), Sort: doc("k", int64(-1)), LimitHint: 10}},
		{"another sort field", backends.QueryParams{Filter: doc("grp", int32(7)), Sort: doc("j", int64(1)), LimitHint: 10}},
		{"another limit", backends.QueryParams{Filter: doc("grp", int32(7)), Sort: doc("k", int64(1)), LimitHint: 20}},
		{"no limit", backends.QueryParams{Filter: doc("grp", int32(7)), Sort: doc("k", int64(1))}},
		{"a hint", backends.QueryParams{Filter: doc("grp", int32(7)), Sort: doc("k", int64(1)), LimitHint: 10, Hint: "grp_1"}},
		{"a collation", backends.QueryParams{Filter: doc("grp", int32(7)), Sort: doc("k", int64(1)), LimitHint: 10, Collated: true, Collation: doc("locale", "fr", "strength", int32(2))}},
		{"fields in another order", backends.QueryParams{Filter: doc("grp", int32(7), "k", int32(1)), Sort: doc("k", int64(1)), LimitHint: 10}},
	}
	seen := map[string]string{shape(base): "base"}
	for _, tc := range differ {
		s := shape(tc.p)
		if prev, dup := seen[s]; dup {
			t.Errorf("%s: shape %q also belongs to %s", tc.name, s, prev)
		}
		seen[s] = tc.name
	}
	if a, b := shape(backends.QueryParams{Filter: doc("grp", int32(7), "k", int32(1))}), shape(backends.QueryParams{Filter: doc("k", int32(1), "grp", int32(7))}); a == b {
		t.Errorf("field order must be kept: both %q", a)
	}

	if a, b := shape(backends.QueryParams{LimitHint: 200}), shape(backends.QueryParams{}); a != b {
		t.Errorf("limits past the trial target share a shape: %q vs %q", a, b)
	}

	in := func(v ...any) backends.QueryParams {
		return backends.QueryParams{Filter: doc("grp", doc("$in", arr(v...)))}
	}
	if a, b := shape(in(int32(1), int32(2))), shape(in(int32(3), int32(4), 5.0)); a != b {
		t.Errorf("$in lists of one class share a shape: %q vs %q", a, b)
	}
	if a, b := shape(in(int32(1))), shape(in(int32(1), "x")); a == b {
		t.Errorf("$in lists of different classes differ: both %q", a)
	}

	or := func(v any) backends.QueryParams {
		return backends.QueryParams{Filter: doc("$or", arr(doc("a", v), doc("b", doc("$gt", int32(1)))))}
	}
	if a, b := shape(or(int32(1))), shape(or(int32(2))); a != b {
		t.Errorf("$or branches compare by shape: %q vs %q", a, b)
	}
	if a, b := shape(or(int32(1))), shape(or("x")); a == b {
		t.Errorf("$or branch value classes differ: both %q", a)
	}
}
