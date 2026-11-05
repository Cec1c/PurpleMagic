# Inventory editing

Inventory changes use a signed preview and one confirmation. Ordinary edits require the selected character to be offline; shared account assets require the account to be offline. Successful changes and their permanent receipts commit together. Retrying a request recovers its original receipt.

When the game implements `inventory-edit-lease-v1`, the confirmation dialog offers **Force execution**. It disconnects the selected account, revokes its old login tokens, waits for all connections to finish saving, and temporarily prevents that account from entering the game. The gateway then applies the reviewed inventory change and releases the account. Other accounts continue playing. The lease expires after one minute if its owner disappears.

Force execution keeps item identity, revision, PVF, quantity and receipt validation. If the item changed before saving finished, refresh it and review the new values. The game does not create a ban or change QQ bindings for this operation. Older game services keep ordinary inventory editing and do not display the force option.

The private runtime API uses four actions with `accountId` and `inventoryEdit.token`: `inventory_edit_begin`, `inventory_edit_status`, `inventory_edit_release`, plus the read-only `inventory_edit_capability` action. A status response includes the protocol, account, token, expiry and `ready`. Readiness means full save/cleanup completion, not merely socket closure. Admission checks and lease creation share the same runtime lock; the existing account database fence protects the final transaction against a new login.

Shared inventory ownership checks no longer acquire a character write lock. Receipt recovery reads the committed receipt without asset locks. MySQL deadlock 1213 retries the whole rolled-back inventory transaction at most twice; connection failures, lock timeouts and ambiguous commit results keep the original request for recovery.

The console's operation history supports deleting a completed or rejected record, clearing failed records, and clearing all finished records. This changes only the current tab's persisted history; permanent business receipts remain on the server. In-flight requests and unknown outcomes keep their original request IDs for recovery. An expired preview or an online-edit rejection can be previewed again before a new confirmation. Repreview preserves the reviewed item revisions and quantities, so an intervening item change still requires a fresh edit.
