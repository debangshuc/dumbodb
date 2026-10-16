# dumboBackup Verification

> **Automated equivalent:** `tests/verify/backup_test.go`
> Run with `go test ./tests/verify/ -run TestBackupVerify -count=1 -timeout=5m`

Back up a database on one server and restore it on another. A backup holds the
database's whole state -- every branch, tag and working set -- so uncommitted
changes come across too. See `docs/COMMANDS.md` for the full `dumboBackup`
reference.

## Prerequisites

Two DumboDB servers with separate data directories, and `mongosh`:

```bash
rm -rf /tmp/dumbo-a /tmp/dumbo-b /tmp/dumbo-backup-orders
dumbodb --data-dir /tmp/dumbo-a --port 27017 &
dumbodb --data-dir /tmp/dumbo-b --port 27018 &
```

## 1. Create data on server A

```js
// mongosh mongodb://localhost:27017
var db = db.getSiblingDB("orders")
db.items.insertMany([{ _id: 1 }, { _id: 2 }])
db.runCommand({ dumboCommit: 1, message: "first two" })
db.runCommand({ dumboBranch: 1, action: "add", branch: "feature" })
db.items.insertOne({ _id: 3 })   // left uncommitted
```

## 2. Back it up

```js
var admin = db.getSiblingDB("admin")
admin.runCommand({ dumboBackup: 1, action: "add", db: "orders", name: "offsite", url: "file:///tmp/dumbo-backup-orders" })
admin.runCommand({ dumboBackup: 1, action: "sync", db: "orders", name: "offsite" })
// { url: "file:///tmp/dumbo-backup-orders", root: "<root>", upToDate: false, ok: 1 }
```

## 3. Restore it on server B

```js
// mongosh mongodb://localhost:27018
db.getSiblingDB("admin").runCommand({ dumboBackup: 1, action: "restore", url: "file:///tmp/dumbo-backup-orders", as: "orders" })
// { db: "orders", url: "file:///tmp/dumbo-backup-orders", root: "<root>", ok: 1 }   -- the same root as the sync

db.getSiblingDB("orders").items.countDocuments()           // 3 -- the uncommitted insert came across
db.getSiblingDB("orders@feature").items.countDocuments()   // 2 -- branches came across
db.getSiblingDB("orders").runCommand({ dumboStatus: 1 })   // dirty: true
```
