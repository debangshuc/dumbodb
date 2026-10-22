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

package clientconn

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/FerretDB/wire"
	"github.com/golang/snappy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dolthub/dumbodb/internal/clientconn/netstats"
)

func encodeMessage(t *testing.T, msg *wire.OpMsg, requestID int32, compressor Compressor) []byte {
	t.Helper()
	body, err := msg.MarshalBinary()
	require.NoError(t, err)
	header := &wire.MsgHeader{
		MessageLength: int32(wire.MsgHeaderLen + len(body)),
		RequestID:     requestID,
		OpCode:        wire.OpCodeMsg,
	}
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	require.NoError(t, writeMessage(w, header, msg, compressor))
	require.NoError(t, w.Flush())
	return buf.Bytes()
}

func readCommand(t *testing.T, raw []byte) (*wire.MsgHeader, string, Compressor) {
	t.Helper()
	header, body, compressor, err := readMessage(bufio.NewReader(bytes.NewReader(raw)))
	require.NoError(t, err)
	doc, err := body.(*wire.OpMsg).RawDocument()
	require.NoError(t, err)
	decoded, err := doc.Decode()
	require.NoError(t, err)
	return header, decoded.Command(), compressor
}

func TestSnappyMessageRoundTrips(t *testing.T) {
	for _, size := range []int{10, 2 << 20} {
		msg := wire.MustOpMsg("insert", "c", "blob", string(make([]byte, size)), "$db", "x")
		raw := encodeMessage(t, msg, 7, CompressorSnappy)
		require.Equal(t, wire.OpCodeCompressed, wire.OpCode(binary.LittleEndian.Uint32(raw[12:16])))
		if size > 1<<20 {
			require.Less(t, len(raw), size/10, "a run of zero bytes must compress")
		}

		header, command, compressor := readCommand(t, raw)
		assert.Equal(t, int32(7), header.RequestID)
		assert.Equal(t, wire.OpCodeMsg, header.OpCode)
		assert.Equal(t, "insert", command)
		assert.Equal(t, CompressorSnappy, compressor)
	}
}

func TestUncompressedMessageReportsNoCompressor(t *testing.T) {
	raw := encodeMessage(t, wire.MustOpMsg("ping", int32(1), "$db", "admin"), 3, CompressorNone)
	require.Equal(t, wire.OpCodeMsg, wire.OpCode(binary.LittleEndian.Uint32(raw[12:16])))
	_, command, compressor := readCommand(t, raw)
	assert.Equal(t, "ping", command)
	assert.Equal(t, CompressorNone, compressor)
}

// compressedFrame builds an OP_COMPRESSED message by hand so tests can lie in
// its prefix.
func compressedFrame(originalOpCode wire.OpCode, uncompressedSize int, compressorID byte, payload []byte) []byte {
	frame := make([]byte, wire.MsgHeaderLen+compressedPrefixLen, wire.MsgHeaderLen+compressedPrefixLen+len(payload))
	binary.LittleEndian.PutUint32(frame[0:4], uint32(cap(frame)))
	binary.LittleEndian.PutUint32(frame[12:16], uint32(wire.OpCodeCompressed))
	binary.LittleEndian.PutUint32(frame[16:20], uint32(originalOpCode))
	binary.LittleEndian.PutUint32(frame[20:24], uint32(uncompressedSize))
	frame[24] = compressorID
	return append(frame, payload...)
}

func TestCompressedMessageRejections(t *testing.T) {
	body, err := wire.MustOpMsg("ping", int32(1), "$db", "admin").MarshalBinary()
	require.NoError(t, err)
	snappyBody := snappy.Encode(nil, body)

	cases := []struct {
		name  string
		frame []byte
		want  string
	}{
		{"noop", compressedFrame(wire.OpCodeMsg, len(body), 0, body), "unhandled opcode OP_COMPRESSED"},
		{"zlib", compressedFrame(wire.OpCodeMsg, len(body), 2, body), "unhandled opcode OP_COMPRESSED"},
		{"zstd", compressedFrame(wire.OpCodeMsg, len(body), 3, body), "unhandled opcode OP_COMPRESSED"},
		{"size_mismatch", compressedFrame(wire.OpCodeMsg, len(body)+1, 1, snappyBody), "does not decode"},
		{"claims_huge_size", compressedFrame(wire.OpCodeMsg, wire.MaxMsgLen, 1, snappyBody), "out of range"},
		{"not_snappy", compressedFrame(wire.OpCodeMsg, len(body), 1, body), "does not decode"},
		{"nested", compressedFrame(wire.OpCodeCompressed, len(body), 1, snappyBody), "wraps another"},
		{"truncated_prefix", compressedFrame(wire.OpCodeMsg, 0, 1, nil)[:wire.MsgHeaderLen+4], "shorter than its header"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frame := tc.frame
			binary.LittleEndian.PutUint32(frame[0:4], uint32(len(frame)))
			_, _, _, err := readMessage(bufio.NewReader(bytes.NewReader(frame)))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// Definitions follow mongod: logical sizes are whole uncompressed messages,
// physical sizes are what crossed the socket, and compressor sizes exclude the
// header and the OP_COMPRESSED prefix.
func TestMessagesRecordNetworkCounters(t *testing.T) {
	msg := wire.MustOpMsg("insert", "c", "blob", string(make([]byte, 50000)), "$db", "x")
	body, err := msg.MarshalBinary()
	require.NoError(t, err)
	logical := int64(wire.MsgHeaderLen + len(body))

	before := netstats.Snapshot()
	compressed := encodeMessage(t, msg, 1, CompressorSnappy)
	plain := encodeMessage(t, msg, 2, CompressorNone)
	readCommand(t, compressed)
	readCommand(t, plain)
	after := netstats.Snapshot()

	physical := int64(len(compressed))
	payload := physical - wire.MsgHeaderLen - compressedPrefixLen
	assert.Equal(t, int64(2), after.NumRequests-before.NumRequests)
	assert.Equal(t, 2*logical, after.BytesIn-before.BytesIn)
	assert.Equal(t, physical+logical, after.PhysicalBytesIn-before.PhysicalBytesIn)
	assert.Equal(t, 2*logical, after.BytesOut-before.BytesOut)
	assert.Equal(t, physical+logical, after.PhysicalBytesOut-before.PhysicalBytesOut)
	assert.Equal(t, payload, after.SnappyDecompressorBytesIn-before.SnappyDecompressorBytesIn)
	assert.Equal(t, int64(len(body)), after.SnappyDecompressorBytesOut-before.SnappyDecompressorBytesOut)
	assert.Equal(t, int64(len(body)), after.SnappyCompressorBytesIn-before.SnappyCompressorBytesIn)
	assert.Equal(t, payload, after.SnappyCompressorBytesOut-before.SnappyCompressorBytesOut)
}
