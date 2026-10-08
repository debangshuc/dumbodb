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

	"github.com/dolthub/dumbodb/internal/clientconn/conninfo"
	"github.com/dolthub/dumbodb/internal/replication/membership"
	"github.com/dolthub/dumbodb/internal/util/must"
)

func authenticatedCtx(user, db string) context.Context {
	info := conninfo.New()
	info.SetAuth(user, "", nil, db)
	info.SetAuthenticated()
	return conninfo.Ctx(context.Background(), info)
}

// Before the fix replSetGetStatus, replSetGetConfig and replSetGetRBID were
// anonymous, so any client could read the topology, optimes and config even
// under --auth --keyFile.
func TestReplSetInspectionRequiresAuthorization(t *testing.T) {
	h := authGateHandler(t, true)
	h.MembershipCredentials = must.NotFail(membership.New("abcdef"))
	h.initCommands()
	createUserWithRole(t, h, "x", "reader", "read")
	createUserWithRole(t, h, "admin", "monitor", "clusterMonitor")

	anonymous := conninfo.Ctx(context.Background(), conninfo.New())
	reader := authenticatedCtx("reader", "x")
	monitor := authenticatedCtx("monitor", "admin")
	member := authenticatedCtx(membership.Username, membership.Database)

	for _, name := range []string{"replSetGetStatus", "replSetGetConfig", "replSetGetRBID"} {
		_, err := h.commands[name].Handler(anonymous, gateCmd(t, name))
		require.True(t, isForcedLoginError(err), "%s must require authentication, got %v", name, err)

		_, err = h.commands[name].Handler(reader, gateCmd(t, name))
		require.True(t, isUnauthorized(t, err), "%s must require a cluster privilege, got %v", name, err)

		_, err = h.commands[name].Handler(member, gateCmd(t, name))
		require.False(t, isUnauthorized(t, err), "%s must be allowed for cluster members, got %v", name, err)
	}

	for _, name := range []string{"replSetGetStatus", "replSetGetConfig"} {
		_, err := h.commands[name].Handler(monitor, gateCmd(t, name))
		require.False(t, isUnauthorized(t, err), "%s must be allowed for clusterMonitor, got %v", name, err)
	}
	_, err := h.commands["replSetGetRBID"].Handler(monitor, gateCmd(t, "replSetGetRBID"))
	require.True(t, isUnauthorized(t, err), "replSetGetRBID must be internal only, got %v", err)
}
