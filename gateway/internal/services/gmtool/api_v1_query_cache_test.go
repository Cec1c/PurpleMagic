package gmtool

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/adminops"
	dnfpvf "dnf90.local/gateway/internal/modules/dnf/pvf"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

type failedCurrencyProjection struct{ repository.AccountRepository }

func (failedCurrencyProjection) LoadAccountCurrencies(context.Context, []string) (map[string]repository.AccountRecord, error) {
	return nil, errors.New("projection unavailable")
}

func TestV1BatchCurrencyProjectionMySQL(t *testing.T) {
	for _, shards := range []int{1, 2} {
		t.Run(fmt.Sprint(shards), func(t *testing.T) {
			s, _, open := currencyFixture(t, shards)
			ctx := context.Background()
			before, _, err := open().Account.Load(ctx, "wallet:1")
			if err != nil {
				t.Fatal(err)
			}
			before.Metadata["gm_currency_sequence_v1"] = "7"
			for i := range 300 {
				before.Metadata[fmt.Sprint("unrelated_", i)] = strings.Repeat("x", 100)
			}
			if err = open().Account.Save(ctx, before); err != nil {
				t.Fatal(err)
			}
			batch := open().Account.(repository.AccountCurrencyBatchReader)
			rows, err := batch.LoadAccountCurrencies(ctx, []string{"wallet:1", "wallet:2", "wallet:1", "wallet:missing", "WALLET:1"})
			if err != nil || len(rows) != 2 {
				t.Fatal(len(rows), err)
			}
			if len(rows["wallet:1"].Metadata) != 2 || adminops.CurrencyRevision(rows["wallet:1"]) != adminops.CurrencyRevision(before) {
				t.Fatal("projection includes wrong fields")
			}
			caseOnly, err := batch.LoadAccountCurrencies(ctx, []string{"WALLET:1"})
			if err != nil || len(caseOnly) != 0 {
				t.Fatal("case-insensitive account matched", caseOnly, err)
			}
			if rows, err = batch.LoadAccountCurrencies(ctx, nil); err != nil || len(rows) != 0 {
				t.Fatal(rows, err)
			}
			if _, err = batch.LoadAccountCurrencies(ctx, make([]string, 101)); !errors.Is(err, repository.ErrResourceRange) {
				t.Fatal("unbounded batch", err)
			}
			after, _, err := open().Account.Load(ctx, "wallet:1")
			if err != nil || !reflect.DeepEqual(before.Metadata, after.Metadata) {
				t.Fatal("read altered metadata", err)
			}
			token, err := s.startSession()
			if err != nil {
				t.Fatal(err)
			}
			status, body := v1TestCall(t, s, token, "GET", "/api/v1/accounts?limit=20")
			if status != 200 {
				t.Fatal(status, body)
			}
			data := body["data"].([]any)
			if len(data) != 2 || data[0].(map[string]any)["currency"].(map[string]any)["balance"] != "1000" {
				t.Fatal(body)
			}
			s.deps.Repos = func() (repository.Group, bool) {
				g := open()
				g.Account = failedCurrencyProjection{g.Account}
				return g, true
			}
			status, body = v1TestCall(t, s, token, "GET", "/api/v1/accounts?limit=20")
			if status != 200 || len(body["data"].([]any)) != 2 || body["data"].([]any)[0].(map[string]any)["currency"] != nil {
				t.Fatal("optional projection failure broke directory", status, body)
			}
		})
	}
}

func TestV1QueryGenerationAuthentication(t *testing.T) {
	s := &Server{www: http.NotFoundHandler(), sessions: map[string]time.Time{}, cfg: Config{Password: "fixture"}}
	if status, _ := v1TestCall(t, s, "", "GET", "/api/v1/query-generation"); status != 401 {
		t.Fatal(status)
	}
	token, err := s.startSession()
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := v1TestCall(t, s, token, "GET", "/api/v1/query-generation?unexpected=1"); status != 400 {
		t.Fatal(status)
	}
	source := newItemCacheTestSource(readonlyTestSource{})
	s.deps.PVF = func() (dnfpvf.Source, error) { return source, nil }
	status, body := v1TestCall(t, s, token, "GET", "/api/v1/query-generation")
	if status != 200 || body["data"].(map[string]any)["pvfChecksum"] != source.checksum {
		t.Fatal(status, body)
	}
	source.checksum = strings.Repeat("b", 64)
	status, body = v1TestCall(t, s, token, "GET", "/api/v1/query-generation")
	if status != 200 || body["data"].(map[string]any)["pvfChecksum"] != source.checksum {
		t.Fatal("stale generation", body)
	}
}

type countedProfessionReader struct {
	repository.CharacterRepository
	repository.CharacterSummaryReader
	repository.CharacterResourceReader
	repository.ProfessionManagementReader
	calls     int
	changeJob bool
}

func (c *countedProfessionReader) LoadProfessionManagement(ctx context.Context, id string, ids []int64) (repository.ProfessionManagementState, error) {
	c.calls++
	v, err := c.ProfessionManagementReader.LoadProfessionManagement(ctx, id, ids)
	if c.changeJob {
		v.Growth.Character.Job = "255"
	}
	return v, err
}
