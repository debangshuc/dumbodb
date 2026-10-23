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

// Package password provides utilities for password hashing and verification.
package password

import (
	"fmt"
	"io"
)

// Password wraps a password string.
//
// It exist mainly to avoid issues when multiple string parameters are used.
//
// It should be passed by value.
type Password struct {
	p string
}

func WrapPassword(password string) Password {
	return Password{p: password}
}

func (p Password) Password() string {
	return p.p
}

func (p Password) Empty() bool {
	return p.p == ""
}

// Format prints a placeholder for every verb, so the plaintext cannot reach
// errors or logs through fmt.
func (p Password) Format(f fmt.State, _ rune) {
	_, _ = io.WriteString(f, "<redacted>")
}

// SASLprepError reports a password SASLprep rejects, with the ICU error name
// MongoDB reports. It never includes the password or any of its characters.
type SASLprepError struct {
	ICUError string
}

func (e *SASLprepError) Error() string {
	return "Error preflighting normalization: " + e.ICUError
}
