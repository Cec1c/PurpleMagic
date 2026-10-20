## 2026-10-20 launcher diagnostics candidate

Launcher 0.2.19 adds optional local enhanced diagnostics. Read `docs/LAUNCHER_DIAGNOSTICS.md` and `docs/diagnostics/VALIDATION.md` before changing collector layouts, lifecycle or export. Keep full EXE hash gating, read-only process permissions, separate sampling/writer/UI notifications, bounded buffers and the existing launcher UI. This candidate has not been published or deployed; native two-hour gameplay and frame-time acceptance remain open.

# PurpleMagic repository guidance

- Read `docs/FEATURE_LEDGER.md`, `docs/FOUNDATIONS.md`, `docs/ARCHITECTURE.md`, `docs/PROVENANCE.md` and `changlog.md` before choosing a baseline. Preserve existing working-tree changes.
- `gm-console/` is the only active GM frontend. Its accepted orchid glass design is archived in `docs/design/purplemagic-orchid-20261019/`. Preserve the floating sidebar, capsule header, purple/pink light and dark themes, density and functional grouping. Product text states behavior directly.
- `gm-web/` is **FROZEN_DO_NOT_REFERENCE**: keep it only for historical builds and provenance. Do not copy, import or restyle its layout or components. The 86jp reference supplies function semantics, not the current visual design.
- The seven console sections are search, inventory, mail, quests, GM, accounts/characters, and announcements/download sources. Inventory contains bags, equipment, pet equipment, storage, account storage, pets, avatars and emblems.
- Build and package the frontend from `gm-console/dist/`. Node.js is a development/build dependency; the production frontend is static. Its API adapters live in `gm-console/src/api/`.
- `gateway/` and `gm/` are independent Go modules. Follow `gm/AGENTS.md` for changes there. Never import source from a neighboring checkout at build time.
- External GMService owns full PVF indexes, definition lookup, administrator sessions, mail scheduling and benefit scans. Game-side PlayerGateway owns transaction validation, admission checks and durable receipts. Keep public player and private management listeners and authentication separate.
- Use the actual server schema and binary layouts. Preserve rule IDs, fixed recipient snapshots, idempotency keys and permanent receipts. Selecting an account for mail includes all existing characters; character-specific edits check the target character, while shared assets retain account protection.
- PVF generations use the complete hash of the operator-approved file. Rebuild and verify indexes after a hash change; do not replace approved assets automatically.
- Game listeners, game startup and player databases belong to the game service. Forums and public-player snapshots have separate read contracts, authentication and deployment.
- Runtime configuration, keys, profiles, game assets, generated binaries and private reports stay ignored. Use disposable databases for tests; never grant assets to real players for verification.
- Validate Go list/test/vet in both modules, module boundary checks and Windows/Linux builds for extraction or Go changes. Database-affecting changes also require the changed module's `scripts/test_mysql.py` with a fresh disposable database.
- Launcher checks cover Core, EngineTests, PortableTests and Flutter analyze/test. Preserve client directory discovery and channel compatibility; the launcher version is 0.2.19 candidate (based on the released 0.2.18 source).
- New frontend changes require type checking, build, relevant tests and browser checks against isolated fixtures. Keep accepted design references separate from private screenshots and credentials.
- Build, Git publication, deployment and native game acceptance are separate actions. Compatible EXE/PVF/dstr/NPK/profile signatures and downloaded hashes must be verified before dependent server deployment.
- Windows entrypoints use BAT/Python. Retain third-party license and provenance notices.
