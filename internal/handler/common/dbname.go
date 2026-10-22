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

package common

import (
	"fmt"
	"strings"

	"github.com/dolthub/dumbodb/internal/handler/handlererrors"
)

// invalidDatabaseNameChars are the characters MongoDB forbids in the database
// name of every command. User and role _ids are "<db>.<name>", so allowing '.'
// would let one database's principal alias another's. MongoDB also forbids '$'
// but only when creating a database, which the backend already refuses.
const invalidDatabaseNameChars = "/\\. \"\x00"

// ValidateDatabaseName rejects a database name MongoDB would reject for any
// command, with MongoDB's error.
func ValidateDatabaseName(name string) error {
	if i := strings.IndexAny(name, invalidDatabaseNameChars); i >= 0 {
		return handlererrors.NewCommandErrorMsg(
			handlererrors.ErrInvalidNamespace,
			fmt.Sprintf("'%c' is an invalid character in a db name: %s", name[i], name),
		)
	}
	return nil
}
