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

package password

import (
	"fmt"
	"strings"
	"testing"
)

// A Hebrew letter after Latin ones fails the SASLprep bidi rule.
const bidiPassword = "plaintextsecretא"

func TestSASLprepErrorOmitsPassword(t *testing.T) {
	_, err := SCRAMSHA256Hash(WrapPassword(bidiPassword))
	if err == nil {
		t.Fatal("SASLprep accepted a mixed-direction password")
	}
	if strings.Contains(err.Error(), "plaintextsecret") {
		t.Fatalf("error contains the password: %v", err)
	}
	if want := "Error preflighting normalization: U_STRINGPREP_CHECK_BIDI_ERROR"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want it to contain %q", err, want)
	}
}

func TestPasswordNeverFormatsPlaintext(t *testing.T) {
	p := WrapPassword("plaintextsecret")
	for _, verb := range []string{"%s", "%v", "%+v", "%#v", "%q", "%x"} {
		if got := fmt.Sprintf(verb, p); strings.Contains(got, "plaintextsecret") || strings.Contains(got, fmt.Sprintf("%x", "plaintextsecret")) {
			t.Errorf("fmt %s of a Password = %q", verb, got)
		}
	}
}
