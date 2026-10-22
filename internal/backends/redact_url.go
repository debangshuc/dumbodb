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

import "regexp"

// urlCredentialsRe matches the scheme and userinfo of a URL ("https://u:p@").
var urlCredentialsRe = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/?#@\s"']*@`)

// RedactURLCredentials replaces the userinfo of every URL in s, which may hold
// a token in either the user or the password part. Remote URLs are stored and
// used in full; only what is shown to clients and logs is redacted.
func RedactURLCredentials(s string) string {
	return urlCredentialsRe.ReplaceAllString(s, "${1}<redacted>@")
}
