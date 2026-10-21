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
		{"not_an_array", "snappy", nil},
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
