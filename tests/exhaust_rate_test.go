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

package tests

import (
	"bufio"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/FerretDB/wire"
	"github.com/stretchr/testify/require"
)

func sendRawOpMsg(t *testing.T, conn net.Conn, requestID int32, msg *wire.OpMsg) {
	t.Helper()
	body, err := msg.MarshalBinary()
	require.NoError(t, err)
	header := &wire.MsgHeader{MessageLength: int32(wire.MsgHeaderLen + len(body)), RequestID: requestID, OpCode: wire.OpCodeMsg}
	w := bufio.NewWriter(conn)
	require.NoError(t, wire.WriteMessage(w, header, msg))
	require.NoError(t, w.Flush())
}

// Before the fix an exhaust hello with maxAwaitTimeMS:0 made the server stream
// responses as fast as an unauthenticated client could read them. Drivers
// never check a server more often than minHeartbeatFrequencyMS (500ms), so
// the server now spaces exhaust responses at least that far apart.
func TestExhaustHelloIsRateLimited(t *testing.T) {
	env := startDumboDB(t, "--replSet", "rs0")

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", env.Port))
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	r := bufio.NewReader(conn)

	sendRawOpMsg(t, conn, 1, wire.MustOpMsg("hello", int32(1), "$db", "admin"))
	_, body, err := wire.ReadMessage(r)
	require.NoError(t, err)
	raw, err := body.(*wire.OpMsg).RawDocument()
	require.NoError(t, err)
	first, err := raw.Decode()
	require.NoError(t, err)
	topologyVersion := first.Get("topologyVersion")
	require.NotNil(t, topologyVersion)

	exhaust := wire.MustOpMsg(
		"hello", int32(1),
		"topologyVersion", topologyVersion,
		"maxAwaitTimeMS", int64(0),
		"$db", "admin",
	)
	exhaust.Flags = wire.OpMsgFlags(wire.OpMsgExhaustAllowed)
	sendRawOpMsg(t, conn, 2, exhaust)

	responses := 0
	deadline := time.Now().Add(1200 * time.Millisecond)
	require.NoError(t, conn.SetReadDeadline(deadline))
	for time.Now().Before(deadline) {
		if _, _, err = wire.ReadMessage(r); err != nil {
			break
		}
		responses++
	}
	require.GreaterOrEqual(t, responses, 1)
	require.LessOrEqual(t, responses, 4, "exhaust responses must be spaced out")
}
