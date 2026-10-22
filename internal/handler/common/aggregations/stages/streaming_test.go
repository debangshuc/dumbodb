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

package stages_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dolthub/dumbodb/internal/handler/common/aggregations/stages"
	"github.com/dolthub/dumbodb/internal/types"
	"github.com/dolthub/dumbodb/internal/util/iterator"
	"github.com/dolthub/dumbodb/internal/util/must"
)

// countingIterator yields n documents and counts how many were read.
type countingIterator struct {
	n, read int
}

func (it *countingIterator) Next() (struct{}, *types.Document, error) {
	if it.read == it.n {
		return struct{}{}, nil, iterator.ErrIteratorDone
	}
	it.read++
	return struct{}{}, must.NotFail(types.NewDocument(
		"_id", int32(it.read),
		"items", must.NotFail(types.NewArray(int32(1), int32(2))),
		"sub", must.NotFail(types.NewDocument("v", int32(it.read))),
	)), nil
}

func (it *countingIterator) Close() {}

// Before the fix these stages read their whole input before producing any
// output, holding it all in memory, although MongoDB streams them.
func TestStreamingStagesReadInputIncrementally(t *testing.T) {
	doc := func(pairs ...any) *types.Document { return must.NotFail(types.NewDocument(pairs...)) }

	for name, spec := range map[string]*types.Document{
		"$unwind":      doc("$unwind", "$items"),
		"$replaceRoot": doc("$replaceRoot", doc("newRoot", "$sub")),
		"$redact":      doc("$redact", "$$DESCEND"),
	} {
		t.Run(name, func(t *testing.T) {
			stage, err := stages.NewStage(spec)
			require.NoError(t, err)
			input := &countingIterator{n: 1000}
			closer := iterator.NewMultiCloser()
			defer closer.Close()

			out, err := stage.Process(context.Background(), input, closer)
			require.NoError(t, err)
			_, first, err := out.Next()
			require.NoError(t, err)
			require.NotNil(t, first)
			require.LessOrEqual(t, input.read, 1, "producing the first document read %d input documents", input.read)

			produced := 1
			for {
				_, _, err = out.Next()
				if errors.Is(err, iterator.ErrIteratorDone) {
					break
				}
				require.NoError(t, err)
				produced++
			}
			require.Equal(t, 1000, input.read)
			require.NotZero(t, produced)
		})
	}
}

// Before the fix $out collected its whole input into a slice before writing.
// The writer now receives the input itself and writes it as it reads.
func TestOutHandsInputToWriterUnread(t *testing.T) {
	input := &countingIterator{n: 1000}
	readBeforeWrite, written := -1, 0
	writer := func(_ context.Context, _, _ string, docs types.DocumentsIterator) error {
		readBeforeWrite = input.read
		all, err := iterator.ConsumeValues(docs)
		written = len(all)
		return err
	}

	stage, err := stages.NewOutStage(must.NotFail(types.NewDocument("$out", "c")), "db", writer)
	require.NoError(t, err)
	closer := iterator.NewMultiCloser()
	defer closer.Close()
	out, err := stage.Process(context.Background(), input, closer)
	require.NoError(t, err)
	_, err = iterator.ConsumeValues(out)
	require.NoError(t, err)

	require.Equal(t, 0, readBeforeWrite, "$out read its input before handing it to the writer")
	require.Equal(t, 1000, written)
}

// Before the fix $merge collected its whole input and merged it in one call.
func TestMergeWritesInBatches(t *testing.T) {
	input := &countingIterator{n: 2500}
	var batches []int
	var readAtFirstBatch int
	merger := func(_ context.Context, params *stages.MergeParams) error {
		if len(batches) == 0 {
			readAtFirstBatch = input.read
		}
		batches = append(batches, len(params.Docs))
		return nil
	}

	stage, err := stages.NewMergeStage(must.NotFail(types.NewDocument("$merge", "c")), "db", merger)
	require.NoError(t, err)
	closer := iterator.NewMultiCloser()
	defer closer.Close()
	out, err := stage.Process(context.Background(), input, closer)
	require.NoError(t, err)
	_, err = iterator.ConsumeValues(out)
	require.NoError(t, err)

	total := 0
	for _, n := range batches {
		require.LessOrEqual(t, n, stages.MergeBatchSize)
		total += n
	}
	require.Equal(t, 2500, total)
	require.Greater(t, len(batches), 1)
	require.LessOrEqual(t, readAtFirstBatch, stages.MergeBatchSize)
}
