package adminapi

import "time"

const InventoryEditProtocol = "inventory-edit-lease-v1"

// Force editing disconnects the account, then waits for every connection's
// save/cleanup to finish. The token identifies one short-lived editor lease.
type InventoryEditRequest struct {
	Token string `json:"token"`
}

type InventoryEditStatus struct {
	Protocol  string    `json:"protocol"`
	AccountID string    `json:"accountId,omitempty"`
	Token     string    `json:"token,omitempty"`
	Ready     bool      `json:"ready"`
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
}
