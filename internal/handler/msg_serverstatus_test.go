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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dolthub/dumbodb/internal/clientconn/netstats"
	"github.com/dolthub/dumbodb/internal/types"
	"github.com/dolthub/dumbodb/internal/util/must"
)

// Field names, order and int64 types follow mongod 8.0.28 started with
// --networkMessageCompressors snappy.
func TestNetworkServerStatusMatchesMongodShape(t *testing.T) {
	network := networkServerStatus(netstats.Stats{
		BytesIn: 1, BytesOut: 2, PhysicalBytesIn: 3, PhysicalBytesOut: 4, NumRequests: 5,
		SnappyCompressorBytesIn: 6, SnappyCompressorBytesOut: 7,
		SnappyDecompressorBytesIn: 8, SnappyDecompressorBytesOut: 9,
	})
	require.Equal(t, []string{"bytesIn", "bytesOut", "physicalBytesIn", "physicalBytesOut", "numRequests", "compression"}, network.Keys())
	for key, want := range map[string]int64{"bytesIn": 1, "bytesOut": 2, "physicalBytesIn": 3, "physicalBytesOut": 4, "numRequests": 5} {
		require.Equal(t, want, must.NotFail(network.Get(key)), key)
	}
	compression := must.NotFail(network.Get("compression")).(*types.Document)
	require.Equal(t, []string{"snappy"}, compression.Keys())
	snappy := must.NotFail(compression.Get("snappy")).(*types.Document)
	compressor := must.NotFail(snappy.Get("compressor")).(*types.Document)
	decompressor := must.NotFail(snappy.Get("decompressor")).(*types.Document)
	require.Equal(t, int64(6), must.NotFail(compressor.Get("bytesIn")))
	require.Equal(t, int64(7), must.NotFail(compressor.Get("bytesOut")))
	require.Equal(t, int64(8), must.NotFail(decompressor.Get("bytesIn")))
	require.Equal(t, int64(9), must.NotFail(decompressor.Get("bytesOut")))
}
