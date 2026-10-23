#!/usr/bin/env bash
# Usage: docker/smoke-test.sh IMAGE
#
# Starts containers from IMAGE and checks the behavior the image shares with
# the official mongo image: the root user from MONGO_INITDB_* or
# DUMBO_INITDB_* variables (also as _FILE secrets), the --auth bootstrap
# through docker exec mongosh, /docker-entrypoint-initdb.d scripts, running as
# uid 999, and the runtime image contents. Needs docker and sudo.
set -euo pipefail

image=$1
run_id=smoke-$$
containers=()
volumes=()
dirs=()

cleanup() {
	local c v d
	for c in "${containers[@]}"; do docker rm -f "$c" > /dev/null 2>&1 || :; done
	for v in "${volumes[@]}"; do docker volume rm -f "$v" > /dev/null 2>&1 || :; done
	for d in "${dirs[@]}"; do sudo rm -rf "$d" || :; done
}
trap cleanup EXIT

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

pass() {
	echo "ok: $*"
}

new_dir() {
	local d
	d=$(mktemp -d)
	dirs+=("$d")
	printf '%s' "$d"
}

new_volume() {
	local v="$run_id-$1"
	docker volume create "$v" > /dev/null
	volumes+=("$v")
	printf '%s' "$v"
}

# start NAME DOCKER_RUN_ARGS... starts a container and waits for the real
# server; the temporary server used for initialization listens on 127.0.0.1.
start() {
	local name="$run_id-$1"
	shift
	docker run -d --name "$name" -e DUMBODB_NO_METRICS=1 "$@" > /dev/null
	containers+=("$name")
	for _ in $(seq 1 90); do
		if docker logs "$name" 2>&1 | grep -q 'DumboDB server started.*addr=0.0.0.0:27017'; then
			return 0
		fi
		if [ "$(docker inspect -f '{{.State.Running}}' "$name")" != true ]; then
			docker logs "$name" >&2
			fail "$name exited"
		fi
		sleep 1
	done
	docker logs "$name" >&2
	fail "$name did not start"
}

stop() {
	docker rm -f "$run_id-$1" > /dev/null
}

# can_list NAME [USER PASSWORD] reports whether listDatabases succeeds.
can_list() {
	local name="$run_id-$1"
	if [ $# -gt 1 ]; then
		docker exec "$name" mongosh --quiet -u "$2" -p "$3" --authenticationDatabase admin \
			--eval 'db.adminCommand({listDatabases: 1}).ok' > /dev/null 2>&1
	else
		docker exec "$name" mongosh --quiet --eval 'db.adminCommand({listDatabases: 1}).ok' > /dev/null 2>&1
	fi
}

# 1. Root user from MONGO_INITDB_*; the server runs as uid 999.
data=$(new_volume mongo)
start mongo -e MONGO_INITDB_ROOT_USERNAME=root -e MONGO_INITDB_ROOT_PASSWORD=pw -v "$data:/var/lib/dumbodb" "$image"
can_list mongo && fail "unauthenticated listDatabases was allowed"
can_list mongo root pw || fail "the root user cannot log in"
can_list mongo root wrong && fail "a wrong password was accepted"
uid=$(docker exec "$run_id-mongo" bash -c \
	'for s in /proc/[0-9]*/status; do grep -q "^Name:[[:space:]]*dumbodb$" "$s" && awk "/^Uid:/ {print \$2}" "$s"; done')
[ "$uid" = 999 ] || fail "the server runs as uid '$uid', want 999"
pass "root user from MONGO_INITDB_*, server runs as uid 999"

# 2. A restart on the same data does not initialize again.
stop mongo
start mongo2 -e MONGO_INITDB_ROOT_USERNAME=other -e MONGO_INITDB_ROOT_PASSWORD=other -v "$data:/var/lib/dumbodb" "$image"
can_list mongo2 other other && fail "a restart created another root user"
can_list mongo2 root pw || fail "the original root user is gone after a restart"
pass "restart does not initialize again"

# 3. DUMBO_INITDB_* synonyms with a _FILE secret.
secrets=$(new_dir)
printf 'from-file\n' > "$secrets/pw"
chmod 644 "$secrets/pw"
start dumbo -e DUMBO_INITDB_ROOT_USERNAME=admin -e DUMBO_INITDB_ROOT_PASSWORD_FILE=/run/secrets/pw \
	-v "$secrets/pw:/run/secrets/pw:ro" "$image"
can_list dumbo admin from-file || fail "the DUMBO_INITDB_* root user cannot log in"
pass "root user from DUMBO_INITDB_* with a _FILE secret"

# 4. Conflicting MONGO_ and DUMBO_ values refuse to start.
if docker run --rm -e DUMBODB_NO_METRICS=1 -e MONGO_INITDB_ROOT_USERNAME=a -e DUMBO_INITDB_ROOT_USERNAME=b \
	-e MONGO_INITDB_ROOT_PASSWORD=pw "$image" > /dev/null 2>&1; then
	fail "conflicting MONGO_/DUMBO_ variables were accepted"
fi
pass "conflicting variables refuse to start"

# 5. --auth with the localhost exception through docker exec mongosh.
start bootstrap "$image" --auth
can_list bootstrap && fail "--auth allowed unauthenticated listDatabases"
docker exec "$run_id-bootstrap" mongosh --quiet admin \
	--eval 'db.createUser({user: "boot", pwd: "bootpw", roles: ["root"]})' > /dev/null ||
	fail "the localhost exception did not allow the first user"
can_list bootstrap boot bootpw || fail "the bootstrapped user cannot log in"
if docker exec "$run_id-bootstrap" mongosh --quiet admin \
	--eval 'db.createUser({user: "second", pwd: "pw", roles: []})' > /dev/null 2>&1; then
	fail "the localhost exception allowed a second user"
fi
pass "--auth bootstrap through docker exec mongosh"

# 6. /docker-entrypoint-initdb.d scripts run once.
initdb=$(new_dir)
printf 'db.seed.insertOne({from: "init"})\n' > "$initdb/01-seed.js"
chmod -R a+rX "$initdb"
seeded=$(new_volume initdb)
count_seed() {
	docker exec "$run_id-$1" mongosh --quiet -u root -p pw --authenticationDatabase admin app \
		--eval 'print(db.seed.countDocuments())'
}
start initdb -e MONGO_INITDB_ROOT_USERNAME=root -e MONGO_INITDB_ROOT_PASSWORD=pw -e DUMBO_INITDB_DATABASE=app \
	-v "$initdb:/docker-entrypoint-initdb.d:ro" -v "$seeded:/var/lib/dumbodb" "$image"
[ "$(count_seed initdb)" = 1 ] || fail "the init script did not run"
stop initdb
start initdb2 -e MONGO_INITDB_ROOT_USERNAME=root -e MONGO_INITDB_ROOT_PASSWORD=pw -e DUMBO_INITDB_DATABASE=app \
	-v "$initdb:/docker-entrypoint-initdb.d:ro" -v "$seeded:/var/lib/dumbodb" "$image"
[ "$(count_seed initdb2)" = 1 ] || fail "the init script ran again on restart"
pass "/docker-entrypoint-initdb.d scripts run once"

# 7. A root-owned bind mount is handed to uid 999.
mount=$(new_dir)
sudo chown root:root "$mount"
start bindmount -v "$mount:/var/lib/dumbodb" "$image"
owner=$(sudo stat -c %u "$mount")
[ "$owner" = 999 ] || fail "the bind-mounted data directory is owned by $owner, want 999"
pass "root-owned bind mount is handed to uid 999"

# 8. The runtime image has mongosh but not curl.
docker run --rm "$image" mongosh --version > /dev/null || fail "mongosh is missing"
if docker run --rm --entrypoint bash "$image" -c 'command -v curl' > /dev/null 2>&1; then
	fail "curl is in the runtime image"
fi
pass "runtime image has mongosh and no curl"

echo "all checks passed"
