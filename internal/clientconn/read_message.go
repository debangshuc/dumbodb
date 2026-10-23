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
	"fmt"
	"io"
	"time"

	"github.com/FerretDB/wire"

	"github.com/dolthub/dumbodb/internal/clientconn/netstats"
)

// handshakeTimeout bounds TLS negotiation and, for optional TLS, the wait for
// the first bytes that select the protocol.
const handshakeTimeout = 30 * time.Second

// largeMessageThreshold is the declared length above which a message is read
// as its bytes arrive rather than into a buffer of the declared length.
const largeMessageThreshold = 1 << 20

// readMessage reads one wire message, unwrapping OP_COMPRESSED, and returns the
// compressor the request used so the reply can use it too. wire.ReadMessage
// allocates the full declared length (up to wire.MaxMsgLen) as soon as the
// header arrives, so a client sending only headers could pin that much per
// connection. Large and compressed messages are first read into a buffer that
// grows with the bytes actually received.
//
// The wire parser can panic on some malformed messages, such as a checksummed
// OP_MSG with no room left for the checksum; those are returned as errors, so
// the connection closes without logging a stack trace.
func readMessage(r *bufio.Reader) (header *wire.MsgHeader, body wire.MsgBody, compressor Compressor, err error) {
	defer func() {
		if p := recover(); p != nil {
			header, body, compressor, err = nil, nil, CompressorNone, fmt.Errorf("malformed message: %v", p)
		}
	}()

	prefix, err := r.Peek(wire.MsgHeaderLen)
	if err != nil {
		h, body, err := wire.ReadMessage(r)
		if err == nil {
			netstats.RecordRequest(int64(h.MessageLength), int64(h.MessageLength))
		}
		return h, body, CompressorNone, err
	}

	length := int64(binary.LittleEndian.Uint32(prefix[0:4]))
	compressed := wire.OpCode(binary.LittleEndian.Uint32(prefix[12:16])) == wire.OpCodeCompressed
	if length > wire.MaxMsgLen || (length <= largeMessageThreshold && !compressed) {
		h, body, err := wire.ReadMessage(r)
		if err == nil {
			netstats.RecordRequest(length, length)
		}
		return h, body, CompressorNone, err
	}

	var buf bytes.Buffer
	if _, err = io.CopyN(&buf, r, length); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, nil, CompressorNone, err
	}

	message := buf.Bytes()
	compressor = CompressorNone
	if compressed {
		if message, compressor, err = decompressMessage(message); err != nil {
			return nil, nil, CompressorNone, err
		}
	}
	h, msgBody, err := wire.ReadMessage(bufio.NewReader(bytes.NewReader(message)))
	if err != nil {
		return h, msgBody, compressor, err
	}
	netstats.RecordRequest(int64(len(message)), length)
	if compressor == CompressorSnappy {
		netstats.RecordSnappyDecompression(length-wire.MsgHeaderLen-compressedPrefixLen, int64(len(message)-wire.MsgHeaderLen))
	}
	return h, msgBody, compressor, nil
}
