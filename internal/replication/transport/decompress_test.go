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


package transport

import (
	"encoding/binary"
	"runtime"
	"testing"

	"github.com/FerretDB/wire"
	"github.com/golang/snappy"
)

// Before the fix snappy.Decode allocated the length declared in the snappy
// stream (up to 4GiB) before the OP_COMPRESSED size was checked, so a tiny
// reply from a peer could exhaust memory.
func TestDecompressSnappyChecksDeclaredLengthFirst(t *testing.T) {
	encoded := snappy.Encode(nil, []byte("hello"))
	_, prefix := binary.Uvarint(encoded)
	forged := binary.AppendUvarint(nil, 1<<30)
	forged = append(forged, encoded[prefix:]...)

	payload := binary.LittleEndian.AppendUint32(nil, uint32(wire.OpCodeMsg))
	payload = binary.LittleEndian.AppendUint32(payload, uint32(len("hello")))
	payload = append(payload, byte(CompressorSnappy))
	payload = append(payload, forged...)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, _, err := decompress(payload)
	runtime.ReadMemStats(&after)

	if err == nil {
		t.Fatal("decompress accepted a snappy stream longer than the declared size")
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 1<<20 {
		t.Fatalf("decompress allocated %d bytes for a %d-byte message", allocated, len(payload))
	}
}
