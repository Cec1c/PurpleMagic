package testbenefits

import (
	"errors"
	"math"
	"strconv"

	"dnf90.local/gateway/internal/modules/dnf/repository"
)

// The account is loaded under the same row lock used by wallet operations.
// Balance, revision, permanent receipt and notification commit together.
func creditCampaignCera(account *repository.AccountRecord, amount int64) (DeliveryReceipt, error) {
	read := func(key string) (int64, error) {
		v, exists := account.Metadata[key]
		if !exists {
			return 0, nil
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 || strconv.FormatInt(n, 10) != v {
			return 0, errors.New("账号点券余额或版本无效")
		}
		return n, nil
	}
	before, err := read("account_cera")
	if err != nil {
		return DeliveryReceipt{}, err
	}
	seq, err := read("gm_currency_sequence_v1")
	if err != nil {
		return DeliveryReceipt{}, err
	}
	if amount <= 0 || amount > MaxAmount || before > math.MaxInt64-amount || seq == math.MaxInt64 {
		return DeliveryReceipt{}, errors.New("点券余额或版本超过上限")
	}
	if account.Metadata == nil {
		account.Metadata = make(map[string]string)
	}
	result := DeliveryReceipt{Cera: amount, Before: strconv.FormatInt(before, 10), After: strconv.FormatInt(before+amount, 10)}
	account.Metadata["account_cera"] = result.After
	account.Metadata["gm_currency_sequence_v1"] = strconv.FormatInt(seq+1, 10)
	return result, nil
}

func validCeraReceipt(r DeliveryReceipt, amount int64) bool {
	before, e1 := strconv.ParseInt(r.Before, 10, 64)
	after, e2 := strconv.ParseInt(r.After, 10, 64)
	return e1 == nil && e2 == nil && before >= 0 && before <= math.MaxInt64-amount &&
		after == before+amount && r.Cera == amount && !r.GrantedAt.IsZero()
}
