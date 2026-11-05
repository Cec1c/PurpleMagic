# Inventory editing

Inventory changes use a signed preview and one confirmation. Ordinary edits require the selected character to be offline; shared account assets require the account to be offline. Successful changes and their permanent receipts commit together. Retrying a request recovers its original receipt.

The console now uses the global **Carbon mode** setting in System settings; see [system settings](GM_SYSTEM_SETTINGS.md). It writes directly while a character is online, without disconnecting them. Game-memory saves may overwrite those changes.

The legacy API still accepts `force: true` for clients using `inventory-edit-lease-v1`. It disconnects the selected account, revokes its old login tokens, waits for all connections to finish saving, and temporarily prevents that account from entering the game. The gateway then applies the reviewed inventory change and releases the account. Other accounts continue playing. The lease expires after one minute if its owner disappears.

Force execution keeps item identity, revision, PVF, quantity and receipt validation. If the item changed before saving finished, refresh it and review the new values. The game does not create a ban or change QQ bindings for this operation. Carbon mode does not require this game lease API.

The private runtime API uses four actions with `accountId` and `inventoryEdit.token`: `inventory_edit_begin`, `inventory_edit_status`, `inventory_edit_release`, plus the read-only `inventory_edit_capability` action. A status response includes the protocol, account, token, expiry and `ready`. Readiness means full save/cleanup completion, not merely socket closure. Admission checks and lease creation share the same runtime lock; the existing account database fence protects the final transaction against a new login.

Shared inventory ownership checks no longer acquire a character write lock. Receipt recovery reads the committed receipt without asset locks. MySQL deadlock 1213 retries the whole rolled-back inventory transaction at most twice; connection failures, lock timeouts and ambiguous commit results keep the original request for recovery.

The console's operation history supports deleting individual records, clearing failed records, and clearing all finished records. Deletion changes only the current tab's persisted history; it does not cancel a submitted request, and permanent business receipts remain on the server. In-flight requests cannot be removed. Unknown outcomes retain their original IDs for recovery until explicitly removed; bulk cleanup leaves them in place. An expired preview or an online-edit rejection can be previewed again before a new confirmation. Repreview preserves the reviewed item revisions and quantities, so an intervening item change still requires a fresh edit.

Account inventory commits save only the selected slot rows and their extra fields inside the existing account transaction. Other account items are neither deleted nor rewritten. Inventory submission has a 25-second request budget, including the up-to-12-second session drain, leaving time to return through the external GM's 30-second peer limit. Other management writes retain their existing budget. Diagnostics record `inventory.receipt_lookup`, `inventory.drain`, and `inventory.transaction` separately.
