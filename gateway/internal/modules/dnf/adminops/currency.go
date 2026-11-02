package adminops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/cerashop"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

var (
	ErrConflict    = errors.New("operation revision conflict")
	ErrIdempotency = errors.New("idempotency key used for another operation")
	ErrAmount      = errors.New("invalid currency amount")
	ErrAccount     = errors.New("account unavailable")
)

type CurrencyCommand struct {
	AccountID string `json:"accountId"`
	Currency  string `json:"currency"`
	Amount    string `json:"amount"`
	Revision  string `json:"revision"`
	Reason    string `json:"reason"`
	Actor     string `json:"actor"`
	Key       string `json:"-"`
}
type CurrencyReceipt struct {
	RequestID string    `json:"requestId"`
	AccountID string    `json:"accountId"`
	Kind      string    `json:"kind"`
	Currency  string    `json:"currency"`
	Amount    string    `json:"amount"`
	Before    string    `json:"before"`
	After     string    `json:"after"`
	Revision  string    `json:"revision"`
	Reason    string    `json:"reason"`
	Actor     string    `json:"actor"`
	CreatedAt time.Time `json:"createdAt"`
}
type currencyStored struct {
	Receipt     CurrencyReceipt `json:"receipt"`
	ContentHash string          `json:"contentHash"`
}

func CurrencyBalance(a repository.AccountRecord) (int64, error) {
	raw, exists := a.Metadata["account_cera"]
	if !exists {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 || strconv.FormatInt(n, 10) != raw {
		return 0, errors.New("invalid stored currency balance")
	}
	return n, nil
}

func CurrencyRevision(a repository.AccountRecord) string {
	raw, _ := json.Marshal([]any{a.AccountID, a.Metadata["account_cera"], a.Metadata["gm_currency_sequence_v1"]})
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

func AdjustCurrency(ctx context.Context, u repository.AccountManagementUnitOfWork, c CurrencyCommand) (CurrencyReceipt, error) {
	var result CurrencyReceipt
	amount, err := strconv.ParseInt(c.Amount, 10, 64)
	if err != nil || amount == 0 || strconv.FormatInt(amount, 10) != c.Amount || c.Currency != "cera" || len(c.Revision) != 64 || strings.TrimSpace(c.Reason) == "" || len(c.Reason) > 500 || len(c.Key) < 16 || len(c.Key) > 128 || c.AccountID == "" || c.Actor == "" {
		return result, ErrAmount
	}
	if _, err = hex.DecodeString(c.Revision); err != nil {
		return result, ErrAmount
	}
	raw, _ := json.Marshal(c)
	digest := sha256.Sum256(raw)
	identity := hex.EncodeToString(digest[:])
	if u == nil {
		return result, ErrAccount
	}
	err = u.WithinAccountManagement(ctx, c.AccountID, func(accounts repository.AccountRepository, receipts repository.ManagementReceiptStore) error {
		stored, found, e := receipts.Load(ctx, c.Key)
		if e != nil {
			return e
		}
		if found {
			var old currencyStored
			if e = json.Unmarshal(stored, &old); e != nil {
				return e
			}
			if old.ContentHash != identity {
				return ErrIdempotency
			}
			result = old.Receipt
			return nil
		}
		a, found, e := accounts.Load(ctx, c.AccountID)
		if e != nil {
			return e
		}
		if !found || a.AccountID != c.AccountID {
			return ErrAccount
		}
		if CurrencyRevision(a) != c.Revision {
			return ErrConflict
		}
		before, e := CurrencyBalance(a)
		if e != nil {
			return e
		}
		if before < 0 || (amount > 0 && before > math.MaxInt64-amount) || (amount < 0 && (amount == math.MinInt64 || before < -amount)) {
			return ErrAmount
		}
		a = repository.CloneAccount(a)
		cerashop.SetBalance(&a, before+amount)
		seq := int64(0)
		if value := a.Metadata["gm_currency_sequence_v1"]; value != "" {
			seq, e = strconv.ParseInt(value, 10, 64)
			if e != nil || seq < 0 || seq == math.MaxInt64 {
				return errors.New("invalid currency sequence")
			}
		}
		a.Metadata["gm_currency_sequence_v1"] = strconv.FormatInt(seq+1, 10)
		a.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)
		wallet, ok := accounts.(repository.AccountCurrencyWriter)
		if !ok {
			return errors.New("account currency writer unavailable")
		}
		if e = wallet.SaveAccountCurrency(ctx, a); e != nil {
			return e
		}
		result = CurrencyReceipt{RequestID: c.Key, AccountID: c.AccountID, Kind: "currency-adjustment", Currency: "cera", Amount: c.Amount, Before: strconv.FormatInt(before, 10), After: strconv.FormatInt(before+amount, 10), Revision: CurrencyRevision(a), Reason: c.Reason, Actor: c.Actor, CreatedAt: a.UpdatedAt}
		payload, e := json.Marshal(currencyStored{Receipt: result, ContentHash: identity})
		if e != nil {
			return e
		}
		return receipts.Append(ctx, c.Key, payload)
	})
	return result, err
}

func DecodeCurrencyReceipt(raw []byte) (CurrencyReceipt, error) {
	var stored currencyStored
	err := json.Unmarshal(raw, &stored)
	return stored.Receipt, err
}
