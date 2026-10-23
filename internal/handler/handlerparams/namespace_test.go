// Copyright 2021 FerretDB Inc.
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

package handlerparams

import "testing"

// MongoDB splits a namespace at its first dot; collection names may contain
// dots, as in "admin.system.users".
func TestSplitNamespace(t *testing.T) {
	for ns, want := range map[string][2]string{
		"x.c":                {"x", "c"},
		"x.a.b":              {"x", "a.b"},
		"admin.system.users": {"admin", "system.users"},
	} {
		db, coll, err := SplitNamespace(ns, "cmd")
		if err != nil || db != want[0] || coll != want[1] {
			t.Errorf("SplitNamespace(%q) = %q, %q, %v; want %q, %q", ns, db, coll, err, want[0], want[1])
		}
	}
	for _, ns := range []string{"xy", "x.", ".y", ""} {
		if _, _, err := SplitNamespace(ns, "cmd"); err == nil {
			t.Errorf("SplitNamespace(%q) accepted an invalid namespace", ns)
		}
	}
}
