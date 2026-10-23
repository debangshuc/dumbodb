# DumboDB —Document DB with Version Control

[DumboDB](https://github.com/dolthub/dumbodb) is a document database that speaks the
MongoDB 8.0 wire protocol and supports Git-style version control (commit, branch,
merge, diff) over your collections. It's built on the [Dolt](https://github.com/dolthub/dolt)
storage engine.

Connect to DumboDB with any MongoDB driver or `mongosh`:

```bash
mongosh mongodb://localhost:27017
```

> **DumboDB is pre-1.0 software and not yet ready for production use.**
>
> As in MongoDB, access control is off unless you start the server with `--auth`.
> Without it, every connection has full access, so run unauthenticated instances
> only in trusted environments and do not expose them to untrusted networks.

## How to use this image

Running this image with no arguments starts the DumboDB server bound to
`0.0.0.0:27017`, storing data in `/var/lib/dumbodb`:

```bash
docker run -p 27017:27017 dolthub/dumbodb:latest
```

To persist data on the host, mount a volume to `/var/lib/dumbodb`:

```bash
docker run -p 27017:27017 \
  -v /path/on/host:/var/lib/dumbodb \
  dolthub/dumbodb:latest
```

To set flags (e.g. enable auto-commit, change log level), pass them after the
image name. As the mongo image adds `--bind_ip_all`, the entrypoint adds
`--addr 0.0.0.0:27017` and `--data-dir /var/lib/dumbodb` unless you set them:

```bash
docker run -p 27017:27017 dolthub/dumbodb:latest \
  --auto-commit \
  --log-level debug
```

The image includes `mongosh`, so you can open a shell in a running container:

```bash
docker exec -it <container> mongosh
```

Run `--help` to see all supported flags:

```bash
docker run --rm dolthub/dumbodb:latest --help
```

## Access control

The image sets up access control the way the official MongoDB image does.
Every variable has a `DUMBO_` synonym (`DUMBO_INITDB_ROOT_USERNAME` and so on);
setting both names to different values is an error.

| Variable | Meaning |
| --- | --- |
| `MONGO_INITDB_ROOT_USERNAME` | Creates this user with the `root` role in `admin` and enables `--auth`. |
| `MONGO_INITDB_ROOT_PASSWORD` | The root user's password. Required with the username. |
| `MONGO_INITDB_DATABASE` | The database `/docker-entrypoint-initdb.d` scripts run against (default `test`). |

Any of these can instead be read from a file by appending `_FILE`, as with
Docker secrets (`MONGO_INITDB_ROOT_PASSWORD_FILE=/run/secrets/root-password`).

```bash
docker run -p 27017:27017 \
  -e MONGO_INITDB_ROOT_USERNAME=root \
  -e MONGO_INITDB_ROOT_PASSWORD=secret \
  -v dumbodb-data:/var/lib/dumbodb \
  dolthub/dumbodb:latest
```

Initialization only happens when the data directory holds no databases yet. It
starts a temporary server on `127.0.0.1` inside the container, creates the root
user, runs the scripts in `/docker-entrypoint-initdb.d` in sorted order (`*.sh`
files are sourced, `*.js` files run with `mongosh` against
`MONGO_INITDB_DATABASE`, other files are ignored), stops the temporary server,
and starts the real one. Restarting a container with existing data skips all of
it, so changing the variables later does not change the users.

```bash
docker run -p 27017:27017 \
  -e MONGO_INITDB_ROOT_USERNAME=root \
  -e MONGO_INITDB_ROOT_PASSWORD=secret \
  -e MONGO_INITDB_DATABASE=app \
  -v /path/to/scripts:/docker-entrypoint-initdb.d:ro \
  dolthub/dumbodb:latest
```

Alternatively, start with `--auth` and create the first user yourself through
the localhost exception, which allows one user to be created from inside the
container while none exist:

```bash
docker run -d --name dumbodb -p 27017:27017 dolthub/dumbodb:latest --auth
docker exec -it dumbodb mongosh admin \
  --eval 'db.createUser({user: "root", pwd: passwordPrompt(), roles: ["root"]})'
```

## Building the image

The Dockerfile supports three modes via the `DUMBODB_VERSION` build arg:

### Build a specific released version

```bash
docker build -f docker/Dockerfile --build-arg DUMBODB_VERSION=0.1.0 \
  -t dumbodb:0.1.0 .
```

### Build the latest released version

```bash
docker build -f docker/Dockerfile --build-arg DUMBODB_VERSION=latest \
  -t dumbodb:latest .
```

Release builds verify the downloaded binary against the release's
`SHA256SUMS` and fail if it does not match, so a version must come from a
release that publishes checksums.

### Build from local source

The build context must contain a `dumbodb/` directory with the source tree. From
a parent directory containing `dumbodb/`:

```bash
docker build -f dumbodb/docker/Dockerfile --build-arg DUMBODB_VERSION=source \
  -t dumbodb:source .
```

> **Warning** When building from source, no other directories in the build
> context may have names starting with `dumbodb` — the Dockerfile uses a
> wildcard (`dumbodb*/`) to conditionally copy the source tree, and additional
> matches will break the build.

## Persisting data

DumboDB stores all data under the directory passed to `--data-dir`. In this
image that's `/var/lib/dumbodb`, declared as a `VOLUME`. Mount a host directory
or a named volume to keep data across container restarts:

```bash
docker volume create dumbodb-data
docker run -p 27017:27017 -v dumbodb-data:/var/lib/dumbodb dolthub/dumbodb:latest
```

### Running as a non-root user

The server runs as the unprivileged `dumbodb` user (uid/gid `999`), as `mongod`
does in the official MongoDB image. On startup the entrypoint, still root, hands
ownership of `/var/lib/dumbodb` to `dumbodb`, so a root-owned host directory
works as a bind mount, and then drops privileges. Files on the host will be
owned by uid `999`.

To run under a different uid, pass `--user`; the entrypoint then changes no
ownership, so the data directory must already be writable by that user:

```bash
docker run --user 1000:1000 -p 27017:27017 \
  -v /path/on/host:/var/lib/dumbodb \
  dolthub/dumbodb:latest
```

## Version control commands

DumboDB exposes Git-style operations as MongoDB `runCommand` calls
(`dumboCommit`, `dumboBranch`, `dumboMerge`, `dumboLog`, etc.). See the
[Command Reference](https://github.com/dolthub/dumbodb/wiki/Commands) for the
full list and the [project README](https://github.com/dolthub/dumbodb#example-usage)
for examples.
