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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dolthub/dumbodb/internal/clientconn/conninfo"
	"github.com/dolthub/dumbodb/internal/replication/membership"
	"github.com/dolthub/dumbodb/internal/types"
	"github.com/dolthub/dumbodb/internal/util/must"
)

func TestHelloReturnsEmptySASLMechanismArrayForUnknownUser(t *testing.T) {
	handler := authGateHandler(t, false)
	request := must.NotFail(types.NewDocument(
		"hello", int32(1),
		"saslSupportedMechs", "local.__system",
		"$db", "admin",
	))
	response, err := handler.hello(context.Background(), request, "127.0.0.1:27017", "")
	require.NoError(t, err)
	mechanisms, ok := responseValue(response, "saslSupportedMechs").(*types.Array)
	require.True(t, ok)
	require.Zero(t, mechanisms.Len())
}

func TestHelloReturnsInternalMembershipMechanisms(t *testing.T) {
	handler := authGateHandler(t, false)
	credentials, err := membership.New("abcdefghijklmnop")
	require.NoError(t, err)
	handler.MembershipCredentials = credentials
	request := must.NotFail(types.NewDocument(
		"hello", int32(1),
		"saslSupportedMechs", "local.__system",
		"$db", "admin",
	))
	response, err := handler.hello(context.Background(), request, "127.0.0.1:27017", "rs0")
	require.NoError(t, err)
	mechanisms := responseValue(response, "saslSupportedMechs").(*types.Array)
	require.Equal(t, membership.Mechanism, must.NotFail(mechanisms.Get(0)))
	require.Equal(t, membership.MechanismSHA1, must.NotFail(mechanisms.Get(1)))
}

func TestHelloNegotiatesCompressionLikeMongod(t *testing.T) {
	handler := authGateHandler(t, false)
	cases := []struct {
		name      string
		requested any
		want      []any
	}{
		{"all_three", must.NotFail(types.NewArray("snappy", "zlib", "zstd")), []any{"snappy"}},
		{"snappy_last", must.NotFail(types.NewArray("zstd", "zlib", "snappy")), []any{"snappy"}},
		{"none_supported", must.NotFail(types.NewArray("zlib", "lz4")), nil},
		{"empty", types.MakeArray(0), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := must.NotFail(types.NewDocument("hello", int32(1), "compression", tc.requested, "$db", "admin"))
			response, err := handler.hello(context.Background(), request, "127.0.0.1:27017", "")
			require.NoError(t, err)
			if tc.want == nil {
				require.False(t, response.Has("compression"), "mongod omits compression when nothing is negotiated")
				return
			}
			require.Equal(t, "compression", response.Keys()[0], "mongod puts compression first")
			compression := responseValue(response, "compression").(*types.Array)
			require.Equal(t, len(tc.want), compression.Len())
			for i, name := range tc.want {
				require.Equal(t, name, must.NotFail(compression.Get(i)))
			}
		})
	}
}

// Sequences observed against mongod 8.0.28 started with
// --networkMessageCompressors snappy.
func TestHelloRemembersNegotiatedCompressionPerConnection(t *testing.T) {
	handler := authGateHandler(t, false)
	hello := func(t *testing.T, ctx context.Context, command string, compression any) (*types.Document, error) {
		t.Helper()
		request := must.NotFail(types.NewDocument(command, int32(1), "$db", "admin"))
		if compression != nil {
			request.Set("compression", compression)
		}
		return handler.hello(ctx, request, "127.0.0.1:27017", "")
	}
	negotiated := func(t *testing.T, response *types.Document) []any {
		t.Helper()
		if !response.Has("compression") {
			return nil
		}
		array := responseValue(response, "compression").(*types.Array)
		var names []any
		for i := 0; i < array.Len(); i++ {
			names = append(names, must.NotFail(array.Get(i)))
		}
		return names
	}
	snappy := func() *types.Array { return must.NotFail(types.NewArray("snappy")) }

	cases := []struct {
		name     string
		second   any
		command  string
		wantErr  string
		wantThen []any
	}{
		{"no_list_reports_negotiated", nil, "hello", "", []any{"snappy"}},
		{"ismaster_reports_negotiated", nil, "isMaster", "", []any{"snappy"}},
		{"empty_list_clears", types.MakeArray(0), "hello", "", nil},
		{"unsupported_list_clears", must.NotFail(types.NewArray("zlib", "lz4")), "hello", "", nil},
		{"string_is_rejected", "snappy", "hello",
			"BSON field 'hello.compression' is the wrong type 'string', expected type 'array'", []any{"snappy"}},
		{"non_string_element_is_rejected", must.NotFail(types.NewArray(int32(1))), "hello",
			"BSON field 'hello.compression.0' is the wrong type 'int', expected type 'string'", []any{"snappy"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := conninfo.Ctx(context.Background(), conninfo.New())
			response, err := hello(t, ctx, "hello", snappy())
			require.NoError(t, err)
			require.Equal(t, []any{"snappy"}, negotiated(t, response))

			response, err = hello(t, ctx, tc.command, tc.second)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
				if tc.second == nil {
					require.Equal(t, tc.wantThen, negotiated(t, response))
				}
			}

			response, err = hello(t, ctx, "hello", nil)
			require.NoError(t, err)
			require.Equal(t, tc.wantThen, negotiated(t, response), "what a later hello without a list reports")
		})
	}
}
