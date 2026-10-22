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

// Package netstats counts client wire traffic for serverStatus.network, with
// mongod's definitions: logical sizes are whole uncompressed messages, physical
// sizes are what crossed the socket, and compressor sizes exclude the message
// header and the OP_COMPRESSED prefix.
package netstats

import "sync/atomic"

type Stats struct {
	BytesIn          int64
	BytesOut         int64
	PhysicalBytesIn  int64
	PhysicalBytesOut int64
	NumRequests      int64

	SnappyCompressorBytesIn    int64
	SnappyCompressorBytesOut   int64
	SnappyDecompressorBytesIn  int64
	SnappyDecompressorBytesOut int64
}

var (
	bytesIn, bytesOut, physicalBytesIn, physicalBytesOut, numRequests atomic.Int64

	snappyCompressorIn, snappyCompressorOut, snappyDecompressorIn, snappyDecompressorOut atomic.Int64
)

// RecordRequest counts one request read from a client.
func RecordRequest(logicalBytes, physicalBytes int64) {
	numRequests.Add(1)
	bytesIn.Add(logicalBytes)
	physicalBytesIn.Add(physicalBytes)
}

// RecordReply counts one reply written to a client.
func RecordReply(logicalBytes, physicalBytes int64) {
	bytesOut.Add(logicalBytes)
	physicalBytesOut.Add(physicalBytes)
}

// RecordSnappyDecompression counts a request payload decompressed into body.
func RecordSnappyDecompression(payloadBytes, bodyBytes int64) {
	snappyDecompressorIn.Add(payloadBytes)
	snappyDecompressorOut.Add(bodyBytes)
}

// RecordSnappyCompression counts a reply body compressed into payload.
func RecordSnappyCompression(bodyBytes, payloadBytes int64) {
	snappyCompressorIn.Add(bodyBytes)
	snappyCompressorOut.Add(payloadBytes)
}

func Snapshot() Stats {
	return Stats{
		BytesIn:                    bytesIn.Load(),
		BytesOut:                   bytesOut.Load(),
		PhysicalBytesIn:            physicalBytesIn.Load(),
		PhysicalBytesOut:           physicalBytesOut.Load(),
		NumRequests:                numRequests.Load(),
		SnappyCompressorBytesIn:    snappyCompressorIn.Load(),
		SnappyCompressorBytesOut:   snappyCompressorOut.Load(),
		SnappyDecompressorBytesIn:  snappyDecompressorIn.Load(),
		SnappyDecompressorBytesOut: snappyDecompressorOut.Load(),
	}
}
