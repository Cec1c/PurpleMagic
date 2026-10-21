package gmtool

import (
	"net/http"
	"testing"
	"time"
)

func TestV1ItemCatalogKeepsDistinctIDsAndExpiry(t *testing.T) {
	s := &Server{www: http.NotFoundHandler(), sessions: map[string]time.Time{}, cfg: Config{Password: "fixture"}}
	s.items = newItemIndex("", s, func(string, ...any) {})
	s.items.setEntriesLocked([]itemEntry{
		{ID: 101, Name: "觉醒券", Kind: "stackable"},
		{ID: 102, Name: "觉醒券", Kind: "stackable", UsablePeriodDays: 7},
		{ID: 103, Name: "觉醒券", Kind: "stackable", AbsoluteExpireUnix: 2000000000},
	})
	token, err := s.startSession()
	if err != nil {
		t.Fatal(err)
	}
	code, out := v1TestCall(t, s, token, "GET", "/api/v1/items?limit=10")
	if code != 200 {
		t.Fatal(code, out)
	}
	rows := out["data"].(map[string]any)["items"].([]any)
	if len(rows) != 3 {
		t.Fatal(rows)
	}
	values := map[string]map[string]any{}
	for _, raw := range rows {
		row := raw.(map[string]any)
		expiry := row["templateExpiration"].(map[string]any)
		if expiry["known"] != true {
			t.Fatal(expiry)
		}
		values[row["itemId"].(string)] = expiry
	}
	if values["101"]["absoluteExpireTime"] != float64(0) || values["101"]["usablePeriodDays"] != float64(0) || values["102"]["usablePeriodDays"] != float64(7) || values["103"]["absoluteExpireTime"] != float64(2000000000) {
		t.Fatal(values)
	}
}
