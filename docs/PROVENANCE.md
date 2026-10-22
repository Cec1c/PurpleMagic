# Source provenance

`source-manifest.json` records every imported file, its source SHA256 and the
extraction target. Files from the server working snapshot include uncommitted
launcher changes; the server HEAD alone does not identify that snapshot.

- `gm/`: Cec1c/DNF90GM at f5235bc3034492c3f58e07ea3dd85c81c5d228a6. Its own
  NOTICE and pinned compatibility provenance remain under gm/.
- `launcher/`, gateway implementation and selected build helpers: the existing
  ServerS5A3 launcher working snapshot, pinned per file. The legacy WPF Player
  executable and game service application are excluded. Core compatibility and
  legacy signed-update support used by tests remain.
- `gateway/`: playergateway, launcherhub and the migrated management/domain/
  repository packages, using the independent module `dnf90.local/gateway`.
  It includes the MySQL driver and dependencies listed in go.mod. It has no game
  executable or build-time dependency on a neighboring game checkout.

The project was named PurpleMagic on 2026-10-17 and is hosted at
[Cec1c/PurpleMagic](https://github.com/Cec1c/PurpleMagic). Its own project code
is licensed under GNU GPL version 3 only; see [LICENSE](../LICENSE) and
[licensing scope](LICENSING.md). The imported GM snapshot had no top-level
license grant, as recorded in gm/NOTICE.md. Imported code and the launcher's
original notices retain their attribution and licensing status. Reference
projects CPA-Manager-Plus and CPAMP Theme Studio are MIT licensed; the archived
GM Web includes Theme Studio material with its licenses, notices and reference
manifest under gm-web/. The initial import manifest predates this integration work.

Production secrets, player data, PVF, generated binaries and original private
configuration are not part of the source manifest.

The 2026-10-17 independent-repository extraction copied 2,110 source files from
the integration working snapshot, checking SHA256 before and after copying.
That source repository had no initial commit; its working-file manifest is the
extraction identity. See [repository boundaries](REPOSITORY_BOUNDARIES.md).

The 2026-10-17 live toolchain alignment imports launcher 0.2.15 from
`69b41aa2d002b220e275bfd1ccb0789ea39d8a3e` and gateway delivery changes from
`48173f229cb9165ce5cf29c0120654981b5e0c7f`. The verified source archives, original
file hashes and merge decisions are recorded in [the import manifest](live-toolchain-import.json).
The management listener, graceful shutdown, GM routes and announcement revision
checks are retained from PurpleMagic. Deployment profiles and assets remain
external build inputs.

The 2026-10-20 source synchronization includes the accepted Orchid GM Console,
external GM/PVF services and launcher 0.2.16. See [launcher import hashes](launcher-0.2.16-import.json)
and [GM Console attribution](../gm-console/THIRD_PARTY_NOTICES.md). Runtime configuration,
operator credentials, player data and private deployment reports remain outside the repository.

The 0.2.19 diagnostic module ports the reviewed ServerS5A3 read-only AT-5T
sampler at `87861a0` (upstream integration `8ead65a`) into the standalone
launcher engine. The verified layout hashes and added rule/physics evidence
are documented in [launcher diagnostics](LAUNCHER_DIAGNOSTICS.md). The example
archive contains synthetic fixture records; original player samples are excluded.
