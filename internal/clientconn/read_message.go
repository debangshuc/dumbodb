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
	"io"
	"time"

	"github.com/FerretDB/wire"
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
func readMessage(r *bufio.Reader) (*wire.MsgHeader, wire.MsgBody, Compressor, error) {
	header, err := r.Peek(wire.MsgHeaderLen)
	if err != nil {
		h, body, err := wire.ReadMessage(r)
		return h, body, CompressorNone, err
	}

	length := int64(binary.LittleEndian.Uint32(header[0:4]))
	compressed := wire.OpCode(binary.LittleEndian.Uint32(header[12:16])) == wire.OpCodeCompressed
	if length > wire.MaxMsgLen || (length <= largeMessageThreshold && !compressed) {
		h, body, err := wire.ReadMessage(r)
		return h, body, CompressorNone, err
	}

	var buf bytes.Buffer
	if _, err = io.CopyN(&buf, r, length); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, nil, CompressorNone, err
	}

	message, compressor := buf.Bytes(), CompressorNone
	if compressed {
		if message, compressor, err = decompressMessage(message); err != nil {
			return nil, nil, CompressorNone, err
		}
	}
	h, body, err := wire.ReadMessage(bufio.NewReader(bytes.NewReader(message)))
	return h, body, compressor, err
}
