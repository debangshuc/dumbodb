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
	"fmt"
	"strconv"
	"strings"
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
