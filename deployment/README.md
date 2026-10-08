# Deploying coucou

Three ways, differing mainly in who owns the database.

| | [`compose/`](compose) | [`helm/coucou/`](helm/coucou) | [`systemd/`](systemd) |
|---|---|---|---|
| Database | included (postgres 17) | **not included** — point it at one you run | a SQLite file, or a postgres you run |
| Target | one host, homelab, trying it out | a cluster you already operate | one host, the release binary, no containers |
| Settings | `config.toml`, bind-mounted | `values.yaml`, rendered into a `ConfigMap` | `/etc/coucou/config.toml` |
| Secrets | a `.env` file | an existing `Secret`, or inline if you must | `/etc/coucou/env`, `0600 root` |
| Sounds | a bind mount | a PVC, or a hostPath on one node | `/var/lib/coucou/profile/sounds` |

The database split is deliberate. Compose is one host and one lifecycle, so bundling postgres is
honest. A chart that shipped a StatefulSet postgres would be claiming to run your database for you,
and an app chart is the wrong place for that.

## Common to all

**Migrations run themselves.** The bot waits for the database to accept connections and applies
goose migrations at startup, and refuses to start if they fail. There is no migration job to
sequence, in either deployment.

**Slash commands register themselves too.** Every start pushes the current set before the gateway
opens, so a rollout can never serve a command the binary has no handler for. Discord still takes up
to an hour to propagate a *changed* global set — the registration is immediate, the visibility is
not.

Scoping that to one guild is a development-build feature (`go build -tags dev`): guild commands
appear instantly where the global set takes up to an hour, but a development build must not be able
to push half-finished commands to every server. The published image is a release build, so it never
reads `DISCORD_DEV_GUILD`. There is nothing to set here, which is why the chart offers no value
for it.

**One replica, always.** The bot holds a Discord gateway connection. A second instance is a second
bot: it joins the same channels, talks over the first, and writes its own copy of every stats row.
The chart pins `replicas: 1` with a `Recreate` strategy for this reason.

**Endpoints**, all on one port (`ops.http_addr`, default `:9090`):

- `/healthz` — the process is running, and nothing more. It stays 200 through a Discord outage on
  purpose: a liveness probe that checked dependencies would turn someone else's outage into a
  restart loop of your own, and restarting makes a gateway reconnect take *longer*.
- `/readyz` — the gateway is connected and the database answers. 503 otherwise.
- `/metrics` — Prometheus.

The image is `scratch`, so it carries no shell and no curl, and it ships no `HEALTHCHECK` — there is
nothing in it that could run one. Health belongs to whatever is supervising: the chart's probes hit
`/readyz` over HTTP, and compose reports the container running rather than healthy.

## Compose

```sh
cd compose
cp .env.example .env     # fill in DISCORD_BOT_TOKEN and POSTGRES_PASSWORD
cp config.example.toml config.toml   # bot settings; the defaults run as they are
docker compose up -d --build
```

`--build` compiles the image from this checkout. To run a published one instead, set
`COUCOU_IMAGE=ghcr.io/be-sandaa/coucou:0.4.2` in `.env` and drop the flag. Image tags have no
leading `v`, unlike the git tags they are built from.

`PROFILE_DIR` defaults to `../../profile` relative to `compose.yaml`: the bot's character, with its
`profile.toml` (start from `profile.example.toml` at the repo root) and a `sounds/` of pre-encoded
Ogg Opus files. The container runs as uid 65534, so everything in it must be world-readable. A
missing `profile.toml` stops the bot; an empty `sounds/` starts one that never plays anything.

## Helm

The chart renders `config.toml` from `values.yaml` into a `ConfigMap` mounted at `/etc/coucou`. The
token and DSN never go in it: they come from the Secret as environment variables, which the file
references as `${DISCORD_BOT_TOKEN}` and `${DATABASE_URL}`.

The bot's character is the required `profile` value — the contents of its `profile.toml` — rendered
into the same `ConfigMap` and mounted at `/var/lib/coucou/profile/profile.toml`, with the sounds
volume at `/var/lib/coucou/profile/sounds` beside it. `--set-file profile=lenore.toml` is the easy way
to pass it.

Every `v*` tag publishes the chart as an OCI artifact next to the image, versioned with the same
number:

```sh
helm install coucou oci://ghcr.io/be-sandaa/charts/coucou --version 0.4.2 \
  --set discord.existingSecret=coucou-creds \
  --set database.existingSecret=coucou-creds \
  --set-file profile=lenore.toml \
  --set sounds.existingClaim=coucou-sounds
```

Swap the reference for `./helm/coucou` to install this checkout instead. A chart pulled from a tag
carries an `appVersion` that matches a published image; one from a checkout carries whatever
`Chart.yaml` says.

The chart refuses to render without a token and a database URL rather than installing something
that cannot start. Inline `discord.token` / `database.url` work, but they land in your values file
and in Helm's release history — fine for a homelab, wrong anywhere with more than one person.

`metrics.serviceMonitor.enabled=true` needs the Prometheus operator CRDs.

## systemd

`systemd/coucou.service` runs the release binary from `/usr/local/bin` under `DynamicUser=`, with
`/var/lib/coucou` as its state directory. Setup is in the wiki:
[Install as a service](https://github.com/be-sandaa/coucou/wiki/Installation#install-as-a-service).

systemd reads `/etc/coucou/env` as root before dropping privileges, so the bot itself never has read
access to its own secrets file. `systemd-analyze security` rates the unit 1.2 (OK).
