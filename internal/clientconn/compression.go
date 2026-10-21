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
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/FerretDB/wire"
	"github.com/golang/snappy"
)

// Compressor is an OP_COMPRESSED compressor id. CompressorNone marks a message
// that arrived uncompressed; it is not a wire value.
type Compressor int

const (
	CompressorNone   Compressor = -1
	CompressorSnappy Compressor = 1
)

// compressedPrefixLen is the original opcode, uncompressed size and compressor
// id that OP_COMPRESSED puts between its header and the compressed payload.
const compressedPrefixLen = 9

// decompressMessage returns the message an OP_COMPRESSED message carries, with
// its own header. As with mongod started with --networkMessageCompressors
// snappy, snappy is accepted whether or not the client negotiated it, and every
// other compressor, noop included, is refused.
func decompressMessage(compressed []byte) ([]byte, Compressor, error) {
	if len(compressed) < wire.MsgHeaderLen+compressedPrefixLen {
		return nil, CompressorNone, errors.New("OP_COMPRESSED message is shorter than its header")
	}
	prefix := compressed[wire.MsgHeaderLen:]
	originalOpCode := wire.OpCode(binary.LittleEndian.Uint32(prefix[0:4]))
	uncompressedSize := int(int32(binary.LittleEndian.Uint32(prefix[4:8])))
	compressorID := prefix[8]
	payload := prefix[compressedPrefixLen:]

	if Compressor(compressorID) != CompressorSnappy {
		return nil, CompressorNone, fmt.Errorf("unhandled opcode OP_COMPRESSED: unsupported compressor id %d", compressorID)
	}
	if originalOpCode == wire.OpCodeCompressed {
		return nil, CompressorNone, errors.New("OP_COMPRESSED message wraps another OP_COMPRESSED message")
	}
	if uncompressedSize < 0 || uncompressedSize > wire.MaxMsgLen-wire.MsgHeaderLen {
		return nil, CompressorNone, fmt.Errorf("OP_COMPRESSED uncompressed size %d is out of range", uncompressedSize)
	}
	// Checked before decoding so a small payload cannot claim a huge output.
	if decodedLen, err := snappy.DecodedLen(payload); err != nil || decodedLen != uncompressedSize {
		return nil, CompressorNone, fmt.Errorf("OP_COMPRESSED snappy payload does not decode to %d bytes", uncompressedSize)
	}

	message := make([]byte, wire.MsgHeaderLen+uncompressedSize)
	copy(message, compressed[:wire.MsgHeaderLen])
	binary.LittleEndian.PutUint32(message[0:4], uint32(len(message)))
	binary.LittleEndian.PutUint32(message[12:16], uint32(originalOpCode))
	if _, err := snappy.Decode(message[wire.MsgHeaderLen:], payload); err != nil {
		return nil, CompressorNone, fmt.Errorf("decompressing OP_COMPRESSED snappy payload: %w", err)
	}
	return message, CompressorSnappy, nil
}

// writeMessage writes msg uncompressed for CompressorNone, and otherwise as an
// OP_COMPRESSED message with the given compressor, as mongod replies to a
// compressed request.
func writeMessage(w *bufio.Writer, header *wire.MsgHeader, msg wire.MsgBody, compressor Compressor) error {
	if compressor == CompressorNone {
		return wire.WriteMessage(w, header, msg)
	}
	if compressor != CompressorSnappy {
		return fmt.Errorf("unsupported compressor %d", compressor)
	}

	body, err := msg.MarshalBinary()
	if err != nil {
		return err
	}
	payload := snappy.Encode(nil, body)
	compressedHeader := &wire.MsgHeader{
		MessageLength: int32(wire.MsgHeaderLen + compressedPrefixLen + len(payload)),
		RequestID:     header.RequestID,
		ResponseTo:    header.ResponseTo,
		OpCode:        wire.OpCodeCompressed,
	}
	headerBytes, err := compressedHeader.MarshalBinary()
	if err != nil {
		return err
	}
	var prefix [compressedPrefixLen]byte
	binary.LittleEndian.PutUint32(prefix[0:4], uint32(header.OpCode))
	binary.LittleEndian.PutUint32(prefix[4:8], uint32(len(body)))
	prefix[8] = byte(compressor)

	for _, part := range [][]byte{headerBytes, prefix[:], payload} {
		if _, err := w.Write(part); err != nil {
			return err
		}
	}
	return nil
}
