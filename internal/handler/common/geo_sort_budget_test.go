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

package common

import (
	"errors"
	"strings"
	"testing"

	"github.com/dolthub/dumbodb/internal/handler/handlererrors"
	"github.com/dolthub/dumbodb/internal/types"
	"github.com/dolthub/dumbodb/internal/util/iterator"
	"github.com/dolthub/dumbodb/internal/util/must"
)

// $near sorts find results by distance in memory, so like other blocking
// sorts it is limited to StageMemoryLimit rather than unbounded.
func TestGeoDistanceSortHasMemoryLimit(t *testing.T) {
	blob := strings.Repeat("x", 100*1024)
	docs := make([]*types.Document, 1300)
	for i := range docs {
		docs[i] = must.NotFail(types.NewDocument("_id", int32(i), "loc", must.NotFail(types.NewArray(float64(i%10), float64(0))), "blob", blob))
	}

	closer := iterator.NewMultiCloser()
	defer closer.Close()
	_, err := GeoDistanceSortIterator(iterator.Values(iterator.ForSlice(docs)), closer, &GeoSortKey{Field: "loc"})

	var cmdErr *handlererrors.CommandError
	if !errors.As(err, &cmdErr) || cmdErr.Code() != handlererrors.ErrQueryExceededMemoryLimitNoDiskUseAllowed {
		t.Fatalf("err = %v, want QueryExceededMemoryLimitNoDiskUseAllowed", err)
	}
}
