# dumboBackup Verification

> **Automated equivalent:** `tests/verify/backup_test.go`
> Run with `go test ./tests/verify/ -run 'TestBackupVerify|TestBackupDisabledUnderAuth' -count=1 -timeout=5m`

Manual verification guide for `dumboBackup` end-to-end behavior. Work through
each scenario top to bottom; each builds on the previous setup.

`dumboBackup` follows Dolt backups. A backup holds one database's whole root --
every branch, tag and working set -- so uncommitted changes survive a backup and
restore. It is always run against `admin` and names the database with `db`.
See `docs/COMMANDS.md` for the parameter reference.

## Prerequisites

A running DumboDB instance without `--auth` and `mongosh`:

```js
mongosh mongodb://localhost:27017
```

Scenarios write to `file://` backups under `/tmp/dumbo-backup-*`. Remove them
first (`rm -rf /tmp/dumbo-backup-*`) so every sync starts from an empty backup.

---

## Setup: a database with a commit, a tag, a branch and an uncommitted change

```js
var db = db.getSiblingDB("backupvdb")
db.items.insertOne({ _id: 1, label: "committed" })
db.runCommand({ dumboCommit: 1, message: "c1" })
db.runCommand({ dumboTag: 1, name: "v1" })
db.runCommand({ dumboBranch: 1, action: "add", branch: "feature" })

var feature = db.getSiblingDB("backupvdb@feature")
feature.items.insertOne({ _id: 10, label: "feature" })
feature.runCommand({ dumboCommit: 1, message: "f1" })

db.items.insertOne({ _id: 2, label: "uncommitted" })
db.runCommand({ dumboRemote: 1, action: "add", name: "origin", url: "file:///tmp/dumbo-backup-origin" })

var admin = db.getSiblingDB("admin")
```

`backupvdb` has a commit and tag on `main`, a `feature` branch with its own
commit, an uncommitted document on `main`, and an `origin` remote.

---

## Scenario 1: Add and list a backup

```js
admin.runCommand({ dumboBackup: 1, action: "add", db: "backupvdb", name: "nightly", url: "file:///tmp/dumbo-backup-nightly" })
// { name: "nightly", url: "file:///tmp/dumbo-backup-nightly", ok: 1 }

admin.runCommand({ dumboBackup: 1, action: "list", db: "backupvdb" })
// { backups: [ { name: "nightly", url: "file:///tmp/dumbo-backup-nightly" } ], ok: 1 }
```

---

## Scenario 2: Sync, then sync again

```js
const s1 = admin.runCommand({ dumboBackup: 1, action: "sync", db: "backupvdb", name: "nightly" })
// { url: "file:///tmp/dumbo-backup-nightly", root: "<root1>", upToDate: false, ok: 1 }

admin.runCommand({ dumboBackup: 1, action: "sync", db: "backupvdb", name: "nightly" })
// { url: ..., root: "<root1>", upToDate: true, ok: 1 }
```

Nothing changed between the two syncs, so the second is up to date and reports
the same root.

---

## Scenario 3: Restore under a new name

```js
admin.runCommand({ dumboBackup: 1, action: "restore", url: "file:///tmp/dumbo-backup-nightly", as: "backupvdb_copy" })
// { db: "backupvdb_copy", url: ..., root: "<root1>", ok: 1 }

db.getSiblingDB("backupvdb_copy").items.countDocuments()           // 2 -- the uncommitted doc is back
db.getSiblingDB("backupvdb_copy@feature").items.countDocuments()   // 2
db.getSiblingDB("backupvdb_copy").runCommand({ dumboTag: 1 })      // tag v1
db.getSiblingDB("backupvdb_copy").runCommand({ dumboStatus: 1 })   // dirty: true

db.getSiblingDB("backupvdb_copy").runCommand({ dumboRemote: 1, action: "list" })
// { remotes: [], ok: 1 } -- remotes live in admin and are not carried
admin.runCommand({ dumboBackup: 1, action: "list", db: "backupvdb_copy" })
// { backups: [], ok: 1 }
```

---

## Scenario 4: Restore over an existing database

```js
db.getSiblingDB("backupvdb_copy").items.insertOne({ _id: 3 })

admin.runCommand({ dumboBackup: 1, action: "restore", url: "file:///tmp/dumbo-backup-nightly", as: "backupvdb_copy" })
// OperationFailed: dumboBackup restore: database "backupvdb_copy" already exists; use force to replace it
db.getSiblingDB("backupvdb_copy").items.countDocuments()   // 3 -- untouched

admin.runCommand({ dumboBackup: 1, action: "restore", url: "file:///tmp/dumbo-backup-nightly", as: "backupvdb_copy", force: true })
db.getSiblingDB("backupvdb_copy").items.countDocuments()   // 2

admin.runCommand({ dumboUndrop: 1 })
// "dropped" includes backupvdb_copy -- the replaced database can be undropped
```

---

## Scenario 5: Incremental sync

```js
db.items.insertOne({ _id: 4 })
admin.runCommand({ dumboBackup: 1, action: "sync", db: "backupvdb", name: "nightly" })
// { ..., root: "<root2>", upToDate: false, ok: 1 }   -- root2 != root1

admin.runCommand({ dumboBackup: 1, action: "restore", url: "file:///tmp/dumbo-backup-nightly", as: "backupvdb_copy", force: true })
db.getSiblingDB("backupvdb_copy").items.countDocuments()   // 3
```

---

## Scenario 6: Sync to an ad-hoc URL

```js
admin.runCommand({ dumboBackup: 1, action: "sync-url", db: "backupvdb", url: "file:///tmp/dumbo-backup-adhoc" })
admin.runCommand({ dumboBackup: 1, action: "restore", url: "file:///tmp/dumbo-backup-adhoc", as: "backupvdb_adhoc" })
db.getSiblingDB("backupvdb_adhoc").items.countDocuments()   // 3
```

---

## Scenario 7: Removing a backup keeps its data

```js
admin.runCommand({ dumboBackup: 1, action: "remove", db: "backupvdb", name: "nightly" })
admin.runCommand({ dumboBackup: 1, action: "list", db: "backupvdb" })
// { backups: [], ok: 1 }

admin.runCommand({ dumboBackup: 1, action: "restore", url: "file:///tmp/dumbo-backup-nightly", as: "backupvdb_after_remove" })
db.getSiblingDB("backupvdb_after_remove").items.countDocuments()   // 3
```

---

## Scenario 8: Errors

| Command | Expected |
|---------|----------|
| Any `dumboBackup` run against a database other than `admin` | `OperationFailed` |
| `add` / `sync-url` with `db: "admin"` | `OperationFailed` |
| `restore` with `as: "admin"` (even with `force`) | `OperationFailed` |
| `sync-url` to `file:///tmp/dumbo-backup-origin` (a remote's URL) | `OperationFailed` |
| `sync` of a backup name that does not exist | `OperationFailed` |
| `sync-url` of a database that does not exist | `OperationFailed`, and the database is not created |
| `action: "bogus"` | `BadValue` |
| `add` without `url`; `sync` without `db`; `restore` without `as` | `BadValue` |
| An unknown field such as `bogus: 1` | `IDLUnknownField (40415)` |

---

## Scenario 9: Refused under access control

Start the server with `--auth` and connect as a `root` user. Every
`dumboBackup` (and `doltBackup`) action -- `add`, `list`, `remove`, `sync`,
`sync-url`, `restore` -- returns `Unauthorized`.
