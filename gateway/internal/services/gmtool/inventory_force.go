package gmtool

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	"dnf90.local/gateway/internal/modules/dnf/adminops"
)

var errInventoryForceUnavailable = errors.New("forced inventory editing unavailable")
var errInventoryLeaseUnavailable = errors.New("inventory edit lease unavailable")
var errInventoryDraining = errors.New("inventory owner still saving")

func (s *Server) inventoryForceSupported(ctx context.Context) bool {
	if s.deps.Runtime == nil {
		return false
	}
	out, err := s.deps.Runtime.Call(ctx, adminapi.Request{Action: "inventory_edit_capability"})
	return err == nil && out.OK && out.InventoryEdit != nil && out.InventoryEdit.Protocol == adminapi.InventoryEditProtocol
}

func (s *Server) acquireInventoryEdit(ctx context.Context, account string) (adminops.QuestOfflineCheck, func(), error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, nil, err
	}
	token := hex.EncodeToString(raw[:])
	call := func(ctx context.Context, action string) (*adminapi.InventoryEditStatus, error) {
		out, err := s.deps.Runtime.Call(ctx, adminapi.Request{Action: action, AccountID: account, InventoryEdit: &adminapi.InventoryEditRequest{Token: token}})
		if err != nil || !out.OK || out.InventoryEdit == nil || out.InventoryEdit.Protocol != adminapi.InventoryEditProtocol || out.InventoryEdit.AccountID != account || out.InventoryEdit.Token != token {
			return nil, errInventoryLeaseUnavailable
		}
		return out.InventoryEdit, nil
	}
	release := func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = call(cleanup, "inventory_edit_release")
	}
	// Install the lease before disconnecting. Even an ambiguous response is
	// cleaned up with its exact token; a lost release also expires automatically.
	status, err := call(ctx, "inventory_edit_begin")
	if err != nil {
		release()
		return nil, nil, err
	}
	wait, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	for !status.Ready {
		timer := time.NewTimer(150 * time.Millisecond)
		select {
		case <-wait.Done():
			timer.Stop()
			release()
			return nil, nil, errInventoryDraining
		case <-timer.C:
		}
		status, err = call(wait, "inventory_edit_status")
		if err != nil {
			release()
			return nil, nil, err
		}
	}
	check := func(ctx context.Context, _ string) error {
		status, err := call(ctx, "inventory_edit_status")
		if err != nil {
			return err
		}
		if !status.Ready || !status.ExpiresAt.After(time.Now()) {
			return errInventoryDraining
		}
		return nil
	}
	return check, release, nil
}
