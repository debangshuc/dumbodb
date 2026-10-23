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
	"container/list"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dolthub/dumbodb/internal/backends"
	"github.com/dolthub/dumbodb/internal/collation"
	"github.com/dolthub/dumbodb/internal/types"
)

// queryShape returns the plan cache key of params: everything that decides
// which plan Query takes, without the filter's values. Queries differing only
// in filter values share a shape; field order is kept because the planner
// picks the first indexed field.
func queryShape(params *backends.QueryParams) string {
	var b strings.Builder
	b.WriteString("f:")
	writeFilterShape(&b, params.Filter)
	b.WriteString("|s:")
	if params.Sort != nil {
		for _, k := range params.Sort.Keys() {
			fmt.Fprintf(&b, "%q=%v,", k, valueOrNil(params.Sort, k))
		}
	}
	fmt.Fprintf(&b, "|h:%s", hintShape(params.Hint))
	fmt.Fprintf(&b, "|c:%t:%s", params.Collated, collation.Parse(params.Collation).Identity())
	fmt.Fprintf(&b, "|t:%d", trialTarget(params))
	return b.String()
}

// trialTarget is the result count at which the sort-walk trial ends for params.
func trialTarget(params *backends.QueryParams) int {
	if params.LimitHint > 0 && params.LimitHint < trialMaxResults {
		return int(params.LimitHint)
	}
	return trialMaxResults
}

func valueOrNil(doc *types.Document, key string) any {
	v, err := doc.Get(key)
	if err != nil {
		return nil
	}
	return v
}

func writeFilterShape(b *strings.Builder, filter *types.Document) {
	b.WriteByte('{')
	if filter != nil {
		for _, k := range filter.Keys() {
			b.WriteString(strconv.Quote(k))
			b.WriteByte(':')
			writeValueShape(b, valueOrNil(filter, k))
			b.WriteByte(',')
		}
	}
	b.WriteByte('}')
}

// writeValueShape writes v's structure: operator documents and arrays keep
// their operators and element shapes, other values become their type class.
// A $in or $nin list collapses to the set of its element classes.
func writeValueShape(b *strings.Builder, v any) {
	switch val := v.(type) {
	case *types.Document:
		keys := val.Keys()
		if len(keys) == 0 || !strings.HasPrefix(keys[0], "$") {
			b.WriteString("object")
			return
		}
		b.WriteByte('{')
		for _, op := range keys {
			b.WriteString(strconv.Quote(op))
			b.WriteByte(':')
			operand := valueOrNil(val, op)
			if arr, ok := operand.(*types.Array); ok && (op == "$in" || op == "$nin") {
				writeElementClasses(b, arr)
			} else {
				writeValueShape(b, operand)
			}
			b.WriteByte(',')
		}
		b.WriteByte('}')
	case *types.Array:
		b.WriteByte('[')
		for i := 0; i < val.Len(); i++ {
			elem, _ := val.Get(i)
			if doc, ok := elem.(*types.Document); ok {
				writeFilterShape(b, doc)
			} else {
				writeValueShape(b, elem)
			}
			b.WriteByte(',')
		}
		b.WriteByte(']')
	default:
		b.WriteString(valueClass(v))
	}
}

func writeElementClasses(b *strings.Builder, arr *types.Array) {
	seen := map[string]bool{}
	var classes []string
	for i := 0; i < arr.Len(); i++ {
		elem, _ := arr.Get(i)
		var eb strings.Builder
		writeValueShape(&eb, elem)
		if c := eb.String(); !seen[c] {
			seen[c] = true
			classes = append(classes, c)
		}
	}
	fmt.Fprintf(b, "in(%s)", strings.Join(classes, ","))
}

// valueClass groups values the planner bounds alike; numbers share a class.
func valueClass(v any) string {
	switch v.(type) {
	case int32, int64, float64, types.Decimal128:
		return "number"
	case string:
		return "string"
	case nil, types.NullType:
		return "null"
	case bool:
		return "bool"
	case types.ObjectID:
		return "objectId"
	case time.Time:
		return "date"
	case types.Binary:
		return "binData"
	case types.Timestamp:
		return "timestamp"
	case types.Regex:
		return "regex"
	case types.MinKeyType:
		return "minKey"
	case types.MaxKeyType:
		return "maxKey"
	case types.UndefinedType:
		return "undefined"
	}
	return fmt.Sprintf("%T", v)
}

func hintShape(hint any) string {
	switch h := hint.(type) {
	case nil:
		return ""
	case *types.Document:
		var b strings.Builder
		for _, k := range h.Keys() {
			fmt.Fprintf(&b, "%q=%v,", k, valueOrNil(h, k))
		}
		return b.String()
	}
	return fmt.Sprintf("%q", fmt.Sprint(hint))
}

// planCacheMaxEntries bounds the plan cache across all collections.
const planCacheMaxEntries = 10000

// planCacheEntry is the cached outcome of a sort-walk trial: the index the
// walk won on, or empty when the filter's index won.
type planCacheEntry struct {
	walkIndex string
}

// planCache maps a query's collection, index set, and shape to the plan its
// trial chose, so later queries of that shape skip the trial. Least recently
// used entries are evicted past planCacheMaxEntries. The zero value is ready.
type planCache struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	lru     list.List
}

type planCacheItem struct {
	key   string
	entry planCacheEntry
}

func (pc *planCache) get(key string) (planCacheEntry, bool) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	el, ok := pc.entries[key]
	if !ok {
		return planCacheEntry{}, false
	}
	pc.lru.MoveToFront(el)
	return el.Value.(*planCacheItem).entry, true
}

func (pc *planCache) put(key string, entry planCacheEntry) {
	if key == "" {
		return
	}
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.entries == nil {
		pc.entries = make(map[string]*list.Element)
	}
	if el, ok := pc.entries[key]; ok {
		el.Value.(*planCacheItem).entry = entry
		pc.lru.MoveToFront(el)
		return
	}
	pc.entries[key] = pc.lru.PushFront(&planCacheItem{key: key, entry: entry})
	for pc.lru.Len() > planCacheMaxEntries {
		oldest := pc.lru.Back()
		pc.lru.Remove(oldest)
		delete(pc.entries, oldest.Value.(*planCacheItem).key)
	}
}

func (pc *planCache) evict(key string) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if el, ok := pc.entries[key]; ok {
		pc.lru.Remove(el)
		delete(pc.entries, key)
	}
}

// clearPrefix evicts every entry whose key starts with prefix.
func (pc *planCache) clearPrefix(prefix string) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	for key, el := range pc.entries {
		if strings.HasPrefix(key, prefix) {
			pc.lru.Remove(el)
			delete(pc.entries, key)
		}
	}
}

func (pc *planCache) len() int {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	return pc.lru.Len()
}

// planCacheDBPrefix scopes keys to a database on every branch.
func planCacheDBPrefix(db string) string {
	return strconv.Quote(db) + "\x00"
}

// planCacheCollectionPrefix scopes keys to one collection on one branch.
func planCacheCollectionPrefix(db, rootish, coll string) string {
	return planCacheDBPrefix(db) + strconv.Quote(rootish) + "\x00" + strconv.Quote(coll) + "\x00"
}

// planCacheKey identifies params' plan on c. The index fingerprint makes any
// index change miss every earlier entry.
func (c *collection) planCacheKey(idxInfos []backends.IndexInfo, params *backends.QueryParams) string {
	return planCacheCollectionPrefix(c.db.name, c.db.rootish, c.name) + indexFingerprint(idxInfos) + "\x00" + queryShape(params)
}

// indexFingerprint hashes every index definition and planner-visible flag.
func indexFingerprint(idxInfos []backends.IndexInfo) string {
	h := fnv.New128a()
	for _, idx := range idxInfos {
		fmt.Fprintf(h, "%q|", idx.Name)
		for _, kp := range idx.Key {
			fmt.Fprintf(h, "%q:%+v,", kp.Field, kp)
		}
		fmt.Fprintf(h, "|%t|%t|%t|%t|%t|", idx.Unique, idx.Sparse, idx.Hidden, idx.Lossy, idx.Multikey)
		if idx.PartialFilterExpression != nil {
			var b strings.Builder
			writeFilterShape(&b, idx.PartialFilterExpression)
			fmt.Fprintf(h, "%s%v", b.String(), idx.PartialFilterExpression)
		}
		fmt.Fprintf(h, "|%s\n", collation.Parse(idx.Collation).Identity())
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}
