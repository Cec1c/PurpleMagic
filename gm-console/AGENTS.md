# GM Console

- Follow the repository root instructions and `docs/GM_CONSOLE_INTEGRATION_20261019.md`.
- Preserve the accepted orchid glass baseline in `docs/design/purplemagic-orchid-20261019/`: floating navigation, capsule top bar, persistent character context, compact tables and detail panel; light/dark themes remain purple with a pink accent.
- Never use the frozen `gm-web/` or historic prototypes as a visual/component source.
- Keep the accepted sections and eight inventory views. Welfare rules have their own top-level section and work without a selected account or character. Render writes only when supported by the actual server capability and domain contract.
- Use `src/api/` for same-origin Go v1/Hub integration. Keep management tokens out of frontend storage and build-time configuration; authentication uses the server session.
- Preserve fixed-position loading indicators, reduced-motion support, one-confirmation mail flow and operation recovery.
- Run `npm test` and `npm run build`. Use isolated fixtures for browser and live-adapter checks. Package `dist/` through the root packaging script.
