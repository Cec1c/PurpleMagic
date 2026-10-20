package testbenefits_test

import (
	"context"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"dnf90.local/gateway/internal/modules/dnf/repository"
	"dnf90.local/gateway/internal/modules/dnf/testbenefits"
)

func TestCeraBenefitConcurrentRetryAndNotificationDeletion(t *testing.T) {
	g, j, r := fixture(t)
	ctx := context.Background()
	j.Gold, j.Cera, j.Scope, j.CampaignID = 0, 250, testbenefits.ScopeAccount, j.ID
	c, _, err := g.Character.Load(ctx, r.CharacterID)
	require(t, err)
	c.CharacterID, c.Slot = "2", 1
	require(t, g.Character.Save(ctx, c))
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			target := r
			if i%2 == 0 {
				target.CharacterID = "2"
			}
			got, e := testbenefits.DeliverWithReceipt(ctx, g, j, target)
			if e != nil || got.Cera != 250 || got.Before != "0" || got.After != "250" {
				t.Error(got, e)
			}
		}(i)
	}
	wg.Wait()
	a, _, err := g.Account.Load(ctx, r.AccountID)
	require(t, err)
	if a.Metadata["account_cera"] != "250" || a.Metadata["gm_currency_sequence_v1"] != "1" || a.Metadata["unrelated"] != "keep" {
		t.Fatal(a)
	}
	prior, err := testbenefits.PreviousDeliveryReceipt(a, j, r)
	require(t, err)
	box, _, err := g.Mailbox.Load(ctx, prior.CharacterID)
	require(t, err)
	notice := box.Mails[prior.MailID]
	if len(box.Mails) != 1 || notice.Gold != 0 || len(notice.Attachments) != 0 || !strings.Contains(notice.Body, "250") {
		t.Fatal(box)
	}
	require(t, g.Mailbox.Save(ctx, repository.MailboxRecord{CharacterID: prior.CharacterID}))
	again, err := testbenefits.DeliverWithReceipt(ctx, g, j, r)
	require(t, err)
	if !reflect.DeepEqual(again, prior) {
		t.Fatal("receipt changed", again, prior)
	}
	j.Cera++
	if _, err = testbenefits.DeliverWithReceipt(ctx, g, j, r); err == nil {
		t.Fatal("changed reward replay accepted")
	}
}

func TestCeraBenefitRollbackAndInvalidWallet(t *testing.T) {
	for _, raw := range []string{"bad", "-1", "01", "", strconv.FormatInt(math.MaxInt64, 10)} {
		t.Run("balance-"+raw, func(t *testing.T) {
			g, j, r := fixture(t)
			ctx := context.Background()
			j.Gold, j.Cera, j.Scope = 0, 1, testbenefits.ScopeAccount
			a, _, err := g.Account.Load(ctx, r.AccountID)
			require(t, err)
			a.Metadata["account_cera"] = raw
			require(t, g.Account.Save(ctx, a))
			if _, err = testbenefits.DeliverWithReceipt(ctx, g, j, r); err == nil {
				t.Fatal("invalid wallet accepted")
			}
			after, _, err := g.Account.Load(ctx, r.AccountID)
			require(t, err)
			box, _, err := g.Mailbox.Load(ctx, r.CharacterID)
			require(t, err)
			if after.Metadata["account_cera"] != raw || len(box.Mails) != 0 || len(after.Metadata) != len(a.Metadata) {
				t.Fatal("failed credit changed state")
			}
		})
	}
	g, j, r := fixture(t)
	ctx := context.Background()
	j.Gold, j.Cera, j.Scope = 0, 200, testbenefits.ScopeAccount
	original := g.MailboxAssets
	g.MailboxAssets = failingReceipt{original, original.(repository.AccountMailboxCharacterUnitOfWork)}
	if _, err := testbenefits.DeliverWithReceipt(ctx, g, j, r); err == nil {
		t.Fatal("expected injected failure")
	}
	a, _, err := g.Account.Load(ctx, r.AccountID)
	require(t, err)
	box, _, err := g.Mailbox.Load(ctx, r.CharacterID)
	require(t, err)
	if a.Metadata["account_cera"] != "" || len(box.Mails) != 0 {
		t.Fatal("failed transaction committed")
	}
	g.MailboxAssets = original
	_, err = testbenefits.DeliverWithReceipt(ctx, g, j, r)
	require(t, err)
}

func TestCeraBenefitRejectsMixedOrCharacterRewards(t *testing.T) {
	_, j, _ := fixture(t)
	j.Gold, j.Cera, j.Scope = 0, 1, testbenefits.ScopeAccount
	require(t, j.Validate())
	for _, scope := range []testbenefits.GrantScope{"", testbenefits.ScopeCharacter} {
		bad := j
		bad.Scope = scope
		if bad.Validate() == nil {
			t.Fatal("non account scope accepted")
		}
	}
	for _, amount := range []int64{-1, testbenefits.MaxAmount + 1} {
		bad := j
		bad.Cera = amount
		if bad.Validate() == nil {
			t.Fatal("invalid amount")
		}
	}
	j.Gold = 1
	if j.Validate() == nil {
		t.Fatal("mixed reward accepted")
	}
}
