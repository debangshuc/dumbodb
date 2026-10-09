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
	"io"
	"sort"

	"github.com/dolthub/dolt/go/store/prolly"
	"github.com/dolthub/dolt/go/store/prolly/tree"

	"github.com/dolthub/dumbodb/internal/backends"
	"github.com/dolthub/dumbodb/internal/collation"
	"github.com/dolthub/dumbodb/internal/handler/commonpath"
	idxpkg "github.com/dolthub/dumbodb/internal/index"
	"github.com/dolthub/dumbodb/internal/types"
	"github.com/dolthub/dumbodb/internal/util/iterator"
	"github.com/dolthub/dumbodb/internal/util/must"
)

// sortWalkIndex returns the position in infos of an index whose in-order walk
// yields sort's order: its leading field is the single sort field, and it is
// complete (not sparse, partial, or hidden), exact (not lossy, hashed, text, or
// geo), and binary-collated like the query. sort must hold one non-$natural key.
func sortWalkIndex(infos []backends.IndexInfo, sortDoc *types.Document, queryCollation *types.Document) (int, bool) {
	if !sortIsSingleField(sortDoc) || !collation.Parse(queryCollation).IsSimple() {
		return 0, false
	}
	switch must.NotFail(sortDoc.Get(sortDoc.Keys()[0])).(type) {
	case int32, int64, float64:
	default:
		return 0, false
	}
	field := sortDoc.Keys()[0]
	for i, idx := range infos {
		if len(idx.Key) == 0 || idx.Key[0].Field != field {
			continue
		}
		lead := idx.Key[0]
		if lead.Hashed || lead.Text || lead.Geo2D || lead.Geo2DSphere {
			continue
		}
		if idx.Lossy || idx.Sparse || idx.Hidden || idx.PartialFilterExpression != nil {
			continue
		}
		if !collation.Parse(idx.Collation).IsSimple() {
			continue
		}
		return i, true
	}
	return 0, false
}

// trySortWalk serves a single-field sort by walking the index led by the sort
// field in sort order, fetching documents lazily so a downstream limit stops
// the walk. It declines (false) when no index qualifies or when the query is
// hinted onto another index.
func (c *collection) trySortWalk(ctx context.Context, state *dbState, primary prolly.Map, params *backends.QueryParams) (types.DocumentsIterator, bool, error) {
	if params.Collated || params.OnlyRecordIDs {
		return nil, false, nil
	}
	state.mu.RLock()
	infos, maps, err := resolveIndexes(ctx, c, state)
	state.mu.RUnlock()
	if err != nil {
		return nil, false, err
	}
	i, ok := sortWalkIndex(infos, params.Sort, params.Collation)
	if !ok {
		return nil, false, nil
	}
	if params.Hint != nil && backends.MatchHintedIndex(params.Hint, infos) != infos[i].Name {
		return nil, false, nil
	}
	idx, idxMap := infos[i], maps[i]

	ascending := sortDirectionAscending(params.Sort)
	path, err := types.NewPathFromString(idx.Key[0].Field)
	if err != nil {
		return nil, false, nil
	}
	var mapIter prolly.MapIter
	if ascending {
		mapIter, err = idxMap.IterAll(ctx)
	} else {
		mapIter, err = idxMap.IterAllReverse(ctx)
	}
	if err != nil {
		return nil, false, err
	}

	it := &sortWalkIter{
		ctx:     ctx,
		ns:      state.ns,
		primary: primary,
		entries: mapIter,
		path:    path,
		order:   types.Ascending,
	}
	if !ascending {
		it.order = types.Descending
	}
	if idx.Multikey {
		it.seen = make(map[string]struct{})
		if err := it.loadArrayKeyDocs(idxMap); err != nil {
			return nil, false, err
		}
	}
	if state.backend != nil && state.backend.backgroundRP != nil {
		releasePrimary := state.backend.backgroundRP.pinRoot(state.name, primary.HashOf())
		releaseIndex := state.backend.backgroundRP.pinRoot(state.name, idxMap.HashOf())
		it.release = func() {
			releasePrimary()
			releaseIndex()
		}
	}
	return it, true, nil
}

type sortWalkEntry struct {
	prefix []byte
	id     []byte
}

// sortWalkIter yields documents in index order. Subdocument keys share one
// marker in the index, so a marker group is read whole and ordered by value.
// For a multikey index a document is yielded at its first entry, which in an
// ascending walk is its minimum key and in a descending walk its maximum, the
// value MongoDB sorts it by. Documents holding an array key (an empty or
// nested array) are read up front and merged in by sort value, since one
// array marker covers every array and an empty array sorts below all values.
type sortWalkIter struct {
	ctx       context.Context
	ns        tree.NodeStore
	primary   prolly.Map
	entries   prolly.MapIter
	path      types.Path
	order     types.SortType
	seen      map[string]struct{}
	peeked    *sortWalkEntry
	pending   []*types.Document
	arrayDocs []*types.Document
	walked    *types.Document
	walkDone  bool
	release   func()
}

// loadArrayKeyDocs reads the documents holding an array key, ordered by sort
// value, and marks them seen so the walk skips their other entries.
func (it *sortWalkIter) loadArrayKeyDocs(idxMap prolly.Map) error {
	ids, err := idxpkg.ArrayKeyIDs(it.ctx, idxMap)
	if err != nil {
		return err
	}
	for _, id := range ids {
		doc, err := it.fetch(&sortWalkEntry{id: id})
		if err != nil {
			return err
		}
		if doc != nil {
			it.arrayDocs = append(it.arrayDocs, doc)
		}
	}
	it.sortByValue(it.arrayDocs)
	return nil
}

func (it *sortWalkIter) sortsBefore(a, b *types.Document) bool {
	return types.CompareOrderForSort(commonpath.SortKeyValue(a, it.path), commonpath.SortKeyValue(b, it.path), it.order) == types.Less
}

func (it *sortWalkIter) sortByValue(docs []*types.Document) {
	sort.SliceStable(docs, func(i, j int) bool { return it.sortsBefore(docs[i], docs[j]) })
}

func (it *sortWalkIter) nextEntry() (*sortWalkEntry, error) {
	if it.peeked != nil {
		e := it.peeked
		it.peeked = nil
		return e, nil
	}
	const primaryIDLen = 20
	keyDesc := idxpkg.KeyDescriptor()
	for {
		k, _, err := it.entries.Next(it.ctx)
		if err == io.EOF || (err == nil && k == nil) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		composite, ok := keyDesc.GetBytes(0, k)
		if !ok || len(composite) < primaryIDLen+1 {
			continue
		}
		idStart := len(composite) - primaryIDLen
		return &sortWalkEntry{
			prefix: composite[:idStart-1],
			id:     append([]byte(nil), composite[idStart:]...),
		}, nil
	}
}

// fetch returns the entry's document, or nil when it was already yielded or no
// longer exists.
func (it *sortWalkIter) fetch(e *sortWalkEntry) (*types.Document, error) {
	if it.seen != nil {
		if _, dup := it.seen[string(e.id)]; dup {
			return nil, nil
		}
		it.seen[string(e.id)] = struct{}{}
	}
	doc, err := lookupDocFromPrimary(it.ctx, it.primary, it.ns, e.id)
	if err != nil || doc == nil {
		return nil, err
	}
	doc.SetRecordID(keyBytesToRecordID(e.id))
	return doc, nil
}

func (it *sortWalkIter) Next() (struct{}, *types.Document, error) {
	if it.walked == nil && !it.walkDone {
		doc, err := it.nextWalked()
		if err != nil {
			return struct{}{}, nil, err
		}
		it.walked, it.walkDone = doc, doc == nil
	}
	if len(it.arrayDocs) > 0 && (it.walked == nil || it.sortsBefore(it.arrayDocs[0], it.walked)) {
		doc := it.arrayDocs[0]
		it.arrayDocs = it.arrayDocs[1:]
		return struct{}{}, doc, nil
	}
	if it.walked == nil {
		return struct{}{}, nil, iterator.ErrIteratorDone
	}
	doc := it.walked
	it.walked = nil
	return struct{}{}, doc, nil
}

// nextWalked returns the next document in index order, or nil at the end.
func (it *sortWalkIter) nextWalked() (*types.Document, error) {
	for {
		if len(it.pending) > 0 {
			doc := it.pending[0]
			it.pending = it.pending[1:]
			return doc, nil
		}

		e, err := it.nextEntry()
		if err != nil || e == nil {
			return nil, err
		}
		if idxpkg.LeadingKeyIsArray(e.prefix) && it.seen != nil {
			continue
		}

		if !idxpkg.LeadingKeyMergesValues(e.prefix) {
			doc, err := it.fetch(e)
			if err != nil {
				return nil, err
			}
			if doc != nil {
				return doc, nil
			}
			continue
		}

		if err := it.readMarkerGroup(e); err != nil {
			return nil, err
		}
	}
}

// readMarkerGroup buffers every document whose leading key shares first's
// marker and orders them by their sort value.
func (it *sortWalkIter) readMarkerGroup(first *sortWalkEntry) error {
	marker := first.prefix[0]
	var group []*types.Document
	for e := first; e != nil; {
		doc, err := it.fetch(e)
		if err != nil {
			return err
		}
		if doc != nil {
			group = append(group, doc)
		}
		if e, err = it.nextEntry(); err != nil {
			return err
		}
		if e != nil && (len(e.prefix) == 0 || e.prefix[0] != marker) {
			it.peeked = e
			break
		}
	}
	it.sortByValue(group)
	it.pending = group
	return nil
}

func (it *sortWalkIter) Close() {
	if it.release != nil {
		it.release()
	}
}
