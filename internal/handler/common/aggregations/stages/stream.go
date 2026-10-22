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
	"github.com/dolthub/dumbodb/internal/types"
	"github.com/dolthub/dumbodb/internal/util/iterator"
)

// mapIterator transforms each input document as it is read, so a stage that
// MongoDB streams holds one document at a time rather than its whole input.
// A nil result drops the document.
type mapIterator struct {
	iter      types.DocumentsIterator
	transform func(*types.Document) (*types.Document, error)
}

func newMapIterator(iter types.DocumentsIterator, closer *iterator.MultiCloser, transform func(*types.Document) (*types.Document, error)) types.DocumentsIterator { //nolint:lll // for readability
	res := &mapIterator{iter: iter, transform: transform}
	closer.Add(res)
	return res
}

func (it *mapIterator) Next() (struct{}, *types.Document, error) {
	for {
		_, doc, err := it.iter.Next()
		if err != nil {
			return struct{}{}, nil, err
		}
		out, err := it.transform(doc)
		if err != nil {
			return struct{}{}, nil, err
		}
		if out != nil {
			return struct{}{}, out, nil
		}
	}
}

func (it *mapIterator) Close() {
	it.iter.Close()
}
