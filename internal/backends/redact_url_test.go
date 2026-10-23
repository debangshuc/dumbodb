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

package backends

import "testing"

func TestRedactURLCredentials(t *testing.T) {
	for in, want := range map[string]string{
		"git+https://user:token@github.com/org/repo.git":       "git+https://<redacted>@github.com/org/repo.git",
		"https://token@doltremoteapi.dolthub.com/org/db":       "https://<redacted>@doltremoteapi.dolthub.com/org/db",
		`dumboClone: remote "https://u:p@h/x" has no branches`: `dumboClone: remote "https://<redacted>@h/x" has no branches`,
		"file:///var/backups/db":                               "file:///var/backups/db",
		"https://doltremoteapi.dolthub.com/org/db":             "https://doltremoteapi.dolthub.com/org/db",
		"git@github.com:org/repo.git":                          "git@github.com:org/repo.git",
		"two https://a:b@h1/x and s3://k:s@bucket/y":           "two https://<redacted>@h1/x and s3://<redacted>@bucket/y",
	} {
		if got := RedactURLCredentials(in); got != want {
			t.Errorf("RedactURLCredentials(%q) = %q, want %q", in, got, want)
		}
	}
}
