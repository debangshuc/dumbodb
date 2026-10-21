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
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"log/slog"
	"testing"

	"github.com/FerretDB/wire"
	"github.com/FerretDB/wire/wirebson"
	"github.com/stretchr/testify/require"

	"github.com/dolthub/dumbodb/internal/clientconn/conninfo"
	"github.com/dolthub/dumbodb/internal/handler"
)

func rawMessage(opCode wire.OpCode, body []byte) []byte {
	header := make([]byte, wire.MsgHeaderLen)
	binary.LittleEndian.PutUint32(header[0:], uint32(wire.MsgHeaderLen+len(body)))
	binary.LittleEndian.PutUint32(header[4:], 1)
	binary.LittleEndian.PutUint32(header[12:], uint32(opCode))
	return append(header, body...)
}

func rawDocument(t *testing.T) []byte {
	t.Helper()
	raw, err := wirebson.MustDocument("ping", int32(1)).Encode()
	require.NoError(t, err)
	return raw
}

// Before the fix an OP_MSG with only a document sequence passed the wire
// library's checks and then panicked in route, logging a stack trace for
// every such message from an unauthenticated client.
func TestRouteRejectsOpMsgWithoutBody(t *testing.T) {
	doc := rawDocument(t)
	section := []byte{1}
	section = binary.LittleEndian.AppendUint32(section, uint32(4+len("docs")+1+len(doc)))
	section = append(section, "docs\x00"...)
	section = append(section, doc...)
	body := append(make([]byte, 4), section...)

	header, msg, _, err := readMessage(bufio.NewReader(bytes.NewReader(rawMessage(wire.OpCodeMsg, body))))
	require.NoError(t, err)

	c := &conn{mode: NormalMode, l: slog.New(slog.DiscardHandler), h: &handler.Handler{}}
	ctx := conninfo.Ctx(context.Background(), conninfo.New())

	var resBody wire.MsgBody
	var closeConn bool
	require.NotPanics(t, func() { _, resBody, closeConn = c.route(ctx, header, msg) })
	require.False(t, closeConn)

	res, err := resBody.(*wire.OpMsg).RawDocument()
	require.NoError(t, err)
	decoded, err := res.Decode()
	require.NoError(t, err)
	require.Equal(t, int32(40587), decoded.Get("code"))
	require.Equal(t, "OP_MSG messages must have a body", decoded.Get("errmsg"))
}

// Before the fix the wire library read past the end of a checksummed OP_MSG
// whose last section ended where the checksum should have been. The message's
// last four bytes double as the checksum, so the test searches for a document
// whose CRC-32C ends in the two NUL terminators that close it.
func TestReadMessageRejectsMissingChecksum(t *testing.T) {
	table := crc32.MakeTable(crc32.Castagnoli)
	var message []byte
	for nonce := 0; message == nil; nonce++ {
		doc := wirebson.MustDocument("s", fmt.Sprintf("%08x", nonce)+"..")
		raw, err := doc.Encode()
		require.NoError(t, err)

		body := binary.LittleEndian.AppendUint32(nil, uint32(wire.OpMsgChecksumPresent))
		body = append(body, 0)
		body = append(body, raw...)
		candidate := rawMessage(wire.OpCodeMsg, body)

		sum := crc32.Checksum(candidate[:len(candidate)-4], table)
		if sum>>16 == 0 {
			binary.LittleEndian.PutUint16(candidate[len(candidate)-4:], uint16(sum))
			message = candidate
		}
	}

	var err error
	require.NotPanics(t, func() {
		_, _, _, err = readMessage(bufio.NewReader(bytes.NewReader(message)))
	})
	require.Error(t, err)
}

// The wire library only checks a body's length, so its contents can still be
// invalid when route decodes them.
func TestRouteRejectsInvalidBody(t *testing.T) {
	// {ping: <element of unknown type 0x7f>}
	doc := []byte{0, 0, 0, 0, 0x7f, 'p', 'i', 'n', 'g', 0, 1, 0}
	binary.LittleEndian.PutUint32(doc, uint32(len(doc)))
	body := append(make([]byte, 4), 0)
	body = append(body, doc...)

	header, msg, _, err := readMessage(bufio.NewReader(bytes.NewReader(rawMessage(wire.OpCodeMsg, body))))
	require.NoError(t, err)

	c := &conn{mode: NormalMode, l: slog.New(slog.DiscardHandler), h: &handler.Handler{}}
	ctx := conninfo.Ctx(context.Background(), conninfo.New())

	var resBody wire.MsgBody
	require.NotPanics(t, func() { _, resBody, _ = c.route(ctx, header, msg) })

	res, err := resBody.(*wire.OpMsg).RawDocument()
	require.NoError(t, err)
	decoded, err := res.Decode()
	require.NoError(t, err)
	require.Equal(t, float64(0), decoded.Get("ok"))
}
