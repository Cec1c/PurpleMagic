<div align="center">

<img src="gm-console/public/brand/witch.png" alt="PurpleMagic witch" width="144" height="144">

# PurpleMagic

![React](https://img.shields.io/badge/GM-React%20%2B%20TypeScript-61DAFB?style=flat-square)
![Flutter](https://img.shields.io/badge/Launcher-Flutter-02569B?style=flat-square)
![Go](https://img.shields.io/badge/Services-Go-00ADD8?style=flat-square)
![.NET 8](https://img.shields.io/badge/Engine-.NET%208-512BD4?style=flat-square)
![MariaDB](https://img.shields.io/badge/Storage-MariaDB-003545?style=flat-square)
[![Stars](https://img.shields.io/github/stars/Cec1c/PurpleMagic?style=flat-square)](https://github.com/Cec1c/PurpleMagic/stargazers)

**A Windows player launcher, update gateway and GM management toolkit for DNF90.**

One source repository with independently built components, from player login and updates to inventory, mail and benefit management.

[简体中文](README.zh-CN.md) ｜ [Deploy](docs/DEPLOYMENT.md) ｜ [Build a launcher](docs/LAUNCHER_INPUTS.md) ｜ [Benefit rules](#benefit-rules-configure-once-keep-delivering) ｜ [Network and APIs](docs/architecture/NETWORK_AND_CONTRACTS_20261019.md) ｜ [Feature ledger](docs/FEATURE_LEDGER.md) ｜ [Documentation](#documentation)

</div>


GM queries now show base rows before bounded parallel supplements, cancel reads when navigating, and support timeouts and retries. The Query area includes server activities and a PVF item library; the quest library supports selected characters. Settings can load the approved PVF into memory. See [query behavior and PVF modes](docs/GM_QUERY_PERFORMANCE.md).

## What it does

| Workflow | Current capabilities |
| --- | --- |
| Player login | Windows registration and login, client-directory discovery, channel checks and game launch; launcher source version 0.2.18 |
| Updates | Configurable sources, signed manifests and file hashes, progress, pause, installation and rollback; launcher and game-content updates have separate contracts |
| Accounts and characters | Search, cursor pagination, online filtering, account expansion and deduplicated recipient selection |
| Inventory | Eight views for bags, equipment, pet equipment, character/account storage, pets, avatars and emblems; inspect instances and edit supported attributes, replace compatible items or delete selected batches |
| Progression and assets | Adjust level, profession, awakening, SP/TP, gold and premium currency through previewed transactions with durable receipts |
| Game quests | Read character quests and definitions; accept, abandon, mark ready, complete without rewards and reset through management APIs; the console exposes completion and reset actions |
| Mail and benefits | Selecting an account includes all its existing characters; confirm the final recipient list once, then track the durable queue; enabled benefit rules run automatically |
| Announcements and downloads | Edit announcements, update sources and the signed game connection profile through the Hub from the console |
| GM workspace | Orchid glass light/dark themes, floating navigation, page transitions, fixed-position loading indicators and a centered Token form with an optional 30-day session |

The [integration guide](docs/GM_CONSOLE_INTEGRATION_20261019.md) maps each page to the actual Go APIs. Pet growth editing, full avatar attribute changes, emblem sockets, equipment enchantments and token-coupon currency remain outside the current write contracts. The [feature ledger](docs/FEATURE_LEDGER.md) tracks the broader roadmap, including moderation, telemetry and public snapshots.

## Screenshots

### PurpleMagic launcher

![PurpleMagic Windows launcher with account login, announcements and updates](docs/images/launcher.webp)

### Inventory workspace

![Orchid GM console with eight container views, item attributes and batch selection](docs/images/inventory.webp)

Account/character identifiers and local paths in these screenshots have been redacted.

## Benefit rules: configure once, keep delivering

Persistent benefit rules are a central part of PurpleMagic. Define a reward and enable the rule: the background service delivers it to eligible characters. Include future characters to extend the same policy to new arrivals without assembling another mailing list.

![Automatic benefits with future-character enrollment, permanent receipts and pause/resume](docs/images/benefits.webp)

For example, a welcome rule can grant 2,000,000 gold once per character, covering existing characters when activated and new characters afterward. Permanent receipts prevent duplicate grants. Closing the browser, restarting the service or pausing/resuming the rule preserves the same identity and delivery history.

- **Explicit eligibility:** the first automatic activation fixes the existing-character cutoff; future characters are an independent option.
- **Durable deduplication:** mail, attachments and receipts commit together, including across retries and lost responses.
- **Background delivery:** the external scheduler calls the game gateway in bounded batches.
- **Traceable recovery:** pause/resume keeps rule IDs, cutoffs and receipts; deleting mail does not reset eligibility.

The quick gold form is once per character; the domain API also supports once per account. A rule supports gold, account points (点券), or one item type. Use **补发未收到** on the independent Benefits page to start a durable background redelivery pass; permanent receipts skip previously delivered recipients. New accounts receive eligible rewards after their first character is created, and future characters follow the rule’s account/character scope. Daily resets and recurring rewards are not implemented. See [benefit rule mechanics](docs/BENEFIT_RULES.md) for examples and implementation links.

## Service layout

The external GMService owns administrator sessions, PVF definitions, queries, persistent mail queues and benefit scheduling. The game-side PlayerGateway owns database transactions, game admission checks and receipts. Public player traffic and private management traffic use separate listeners and credentials.

| Component | Directory | Output |
| --- | --- | --- |
| Player launcher | `launcher/` | Flutter Windows UI, C# engine, portable installer and self-update components |
| Game-side gateway and Hub | `gateway/` | `PlayerGateway`: player APIs, signing/publication and private management transactions |
| External GM and PVF | `gateway/` | `GMService` and offline `GMIndex`; full PVF indexes stay outside the game service |
| GM Console | `gm-console/` | React/TypeScript static application served under `/console/` |
| Offline tools and migration sources | `gm/` | Original GM tools, PVF tooling and compatibility contracts |

The [network and API brief](docs/architecture/NETWORK_AND_CONTRACTS_20261019.md) separates game TCP, account HTTPS, management tunnels and download sources. It also distinguishes existing APIs from the proposed scheduler, publisher and launcher build workers. Download the repository and open the [offline overview](docs/architecture/overview-20261019.html), [network diagram](docs/architecture/network-20261019.html) or [module diagram](docs/architecture/modules-20261019.html) in a browser.

### Reads, edits and recovery

Accounts, inventory, mailboxes and quests load by page, container and cursor. A PVF generation is identified by its complete SHA256; changing the file requires rebuilding and verifying its indexes. The game gateway requests bounded definitions from the external service.

Management writes use revision checks, idempotency keys and durable receipts. Character-specific edits check the selected character's offline state; shared account assets retain account-level protection. Confirmed mail keeps running after the browser closes and resumes after service restarts. Benefit rules preserve their delivery records across retries and index generations.

## Deployment and gateway integration

The repository ships a [Linux Docker Compose recipe](deploy/selfhost/compose.yaml), multi-stage image and private configuration generator. Supply an existing game server/MariaDB, approved PVF, player profile and certificates, then prepare the service files, build six index categories, initialize additive management tables and start PlayerGateway, GMService and the HTTPS edge.

```text
python3 deploy/selfhost/prepare.py --settings runtime/selfhost-settings.json --output runtime/selfhost
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml build
```

The [deployment guide](docs/DEPLOYMENT.md) covers inputs, indexing, schema initialization, startup, ports and persistent data. The [gateway integration contract](docs/GATEWAY_INTEGRATION.md) describes account APIs, proxy credentials, runtime admission, storage mapping and two-way SSH tunnels.

> [!IMPORTANT]
> The current adapter targets the DNF90/ServerS5A3 login protocol and MySQL save schema. Other game servers need corresponding APIs and repository mappings. Public configuration uses example values; real endpoints, DSNs, tokens and private keys stay in ignored `runtime/` files or outside the checkout.

## Generate a launcher for your game server

The existing command-line pipeline prepares a profile from a connection configuration, approved client, TLS certificate and RSA public key, then builds the Windows launcher:

```text
python scripts/prepare_launcher_profile.py --connection runtime/launcher-input/server.json --client D:/GameClient --certificate runtime/launcher-input/player-fullchain.pem --public-key runtime/launcher-input/update-public.pem --output runtime/launcher-input/profile.json
BUILD_FLUTTER.bat --flutter D:/SDKs/flutter --dotnet dotnet --profile-dir runtime/launcher-input --output dist/PurpleMagic-0.2.18
BUILD_PORTABLE.bat --bundle dist/PurpleMagic-0.2.18.zip --output dist/PurpleMagic-0.2.18.exe --dotnet dotnet
```

The profile tool hashes the EXE/PVF and certificate and preserves explicit channel compatibility settings. Configure the account `ApiUrl`, direct game `GameHost:BootstrapPort` and update sources separately. The Windows application is named PurpleMagic and uses the native folder picker.

The [build and publishing guide](docs/LAUNCHER_INPUTS.md) explains fields, first-time signing keys, SDK/artwork inputs, channel probes and Hub publication. A web generator and Windows build queue are planned.

## Quick preview

Use Node.js 24 and npm:

```text
cd gm-console
npm ci
npm run dev
```

Open [http://127.0.0.1:8778/?demo=1](http://127.0.0.1:8778/?demo=1) for the standalone sample-data preview. The production build uses the real API adapter. For local integration with a disposable database, follow the [GM Console guide](docs/GM_CONSOLE_INTEGRATION_20261019.md); for service deployment configuration, see [external GM services](docs/GM_EXTERNAL_SERVICES.md).

> [!NOTE]
> `gm-console/` is the current frontend and design baseline. The first `gm-web/` is archived as `FROZEN_DO_NOT_REFERENCE`; production packaging uses `gm-console/dist/`.

## Build and check

Toolchain: Node.js 24, Go 1.26.5, Python 3, .NET 8 and Flutter Windows. Launcher release builds also use Visual Studio C++ and NativeAOT.

Run from the repository root after installing the frontend dependencies:

```text
npm --prefix gm-console test
npm --prefix gm-console run build
python scripts/package_gm_web.py
python scripts/check_boundaries.py
python scripts/build_gateway.py linux amd64
```

The static package is `dist/gm-console/PurpleMagic-GM-Console.zip`. `PlayerGateway`, `GMService` and `GMIndex` are written to `dist/gateway/linux-amd64/`; replace `linux` with `windows` for Windows. Build the original GM tools with `python gm/scripts/build.py linux amd64`.

```text
python scripts/verify_component.py gateway
python scripts/verify_component.py gm
python scripts/verify_component.py launcher --dotnet <dotnet8>
python scripts/verify_component.py flutter --flutter <flutter>
python gateway/scripts/test_mysql.py
python gm/scripts/test_mysql.py
```

Database checks create disposable MariaDB containers and require Docker. Build the launcher with `BUILD_FLUTTER.bat --flutter <flutter> --dotnet <dotnet8> --profile-dir <profile-directory> --output <new-output-directory>`. Supply the [signing profile and artwork](docs/LAUNCHER_INPUTS.md), then use `BUILD_PORTABLE.bat` to wrap the complete ZIP.

> [!IMPORTANT]
> Runtime configuration, signing material, game assets and player data are supplied by the deployment environment. Match the player, game-service and GM EXE/PVF/profile combination, then verify the published files in the [documented release order](docs/REPOSITORY_BOUNDARIES.md).

## Documentation

| Topic | References |
| --- | --- |
| Network, interfaces and future modules | [Topology and contracts](docs/architecture/NETWORK_AND_CONTRACTS_20261019.md) · [Architecture](docs/ARCHITECTURE.md) |
| Console and visual baseline | [Real API integration](docs/GM_CONSOLE_INTEGRATION_20261019.md) · [Orchid design archive](docs/design/purplemagic-orchid-20261019/README.md) |
| Features and transactions | [Feature ledger](docs/FEATURE_LEDGER.md) · [API foundations](docs/FOUNDATIONS.md) |
| Management services | [External GM/PVF](docs/GM_EXTERNAL_SERVICES.md) · [Current routes](docs/API_CATALOG.md) · [Mail and benefits](docs/GM_OPERATIONS_V1.md) |
| Items and character data | [PVF item details](docs/GM_ITEM_DETAILS.md) · [Character resources](docs/GM_CHARACTER_RESOURCES.md) |
| Announcements and public queries | [Launcher announcements](docs/LAUNCHER_ANNOUNCEMENTS.md) · [Public snapshot design](docs/PUBLIC_PLAYER_SNAPSHOTS.md) |
| Changes and source versions | [Changelog](changlog.md) · [Provenance](docs/PROVENANCE.md) · [Launcher 0.2.16 import](docs/launcher-0.2.16-import.json) |

## Sources and licenses

Components retain their original licenses and copyright notices. See [source provenance](docs/PROVENANCE.md), the [GM notice](gm/NOTICE.md), [current frontend notices](gm-console/THIRD_PARTY_NOTICES.md) and [archived frontend notices](gm-web/THIRD_PARTY_NOTICES.md).
