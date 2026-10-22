# Self-hosting PurpleMagic

For a step-by-step introduction to solo, LAN and internet play, see the [beginner guide (Chinese)](BIG_FISH_GUIDE.zh-CN.md).

[完整中文部署步骤](DEPLOYMENT.zh-CN.md)

The repository includes a multi-stage Docker image, Linux Compose services and a private configuration generator in `deploy/selfhost/`. It connects to an existing compatible game server and MariaDB schema. The game process and database are managed by the game project.

Use Linux, Docker Engine/Compose v2, Python 3.10+, OpenSSL and Git. The image builds with Node.js 24 and Go 1.26.5. The recipe uses host networking so game and database loopback endpoints remain reachable. Prepare files as a non-root deployment user.

## Configuration

```sh
git clone https://github.com/Cec1c/PurpleMagic.git
cd PurpleMagic
mkdir -p runtime
cp deploy/selfhost/settings.example.json runtime/selfhost-settings.json
chmod 600 runtime/selfhost-settings.json
```

Edit the private copy with your GM HTTPS origin, approved player profile and matching PKCS8 RSA signing key, approved PVF, player/GM TLS certificates and keys, player CA chain, registration/repository DSNs, table mapping, game account API/proxy secret, and game administration API/token. The player's `ApiUrl` in this recipe is a direct HTTPS endpoint such as `https://players.example.com:8443`. The GM origin uses port 443.

```sh
python3 deploy/selfhost/prepare.py --settings runtime/selfhost-settings.json --output runtime/selfhost
```

The generator checks the full PVF hash, profile certificate pin, certificate names/key pairs and signing public key. It creates matching private service tokens and an administrator token. Configuration files use mode `0600`; directories use `0700`. Existing output directories are preserved. `runtime/` is excluded from Git and Docker build inputs. Read the GM token from `runtime/selfhost/admin-token.txt`.

## Build and initialize

Back up the existing game database before initializing additive management tables.

```sh
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml build
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml --profile setup run --rm index-pvf
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml --profile setup run --rm index-items
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml --profile setup run --rm index-quests
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml --profile setup run --rm index-professions
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml --profile setup run --rm schema
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml up -d gateway gm edge
```

The offline steps build all six index categories: PVF directory, item summaries/details, quests, professions and auxiliary professions. They do not connect to the database. Schema initialization requires the existing compatible game tables.

## Verify

Open `https://gm.example.com/console/#query` and log in with the generated administrator token. Check account/character reads, a known item search, and the announcement/source page. GM and Hub use the same administrator token with separate sessions.

Check the player `/api/status` and `/launcher/v2/bootstrap.json` endpoints. Run the [launcher channel probe](LAUNCHER_INPUTS.md) for TLS pinning, directory identity and game-port checks. Inspect `docker compose ... ps` and `logs --tail=80 gateway gm edge`.

Only GM HTTPS 443 and player HTTPS 8443 are public. Management 18444, GM Web 18080 and catalog 18081 bind loopback. Game TCP uses its own bootstrap/channel ports. See the [gateway integration contract](GATEWAY_INTEGRATION.md) for account authentication, game runtime admission and two-host SSH routing.

## Upgrade and persistence

Rebuild and run `up -d` with the same private directory. Keep `gm/` (sessions/queue), `hub/` (signed publication state/news/bundles), signing keys and game database receipts. Recreating a container does not recreate benefit rules. For a new PVF, build a fresh generation and align both peers with the approved EXE/PVF/profile. Roll back application images while retaining current persistent data; publish asset rollbacks with a new sequence.

Configuration tests: `python3 scripts/test_deployment_tools.py`. Compose validation: `docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml --profile setup config --quiet`.
