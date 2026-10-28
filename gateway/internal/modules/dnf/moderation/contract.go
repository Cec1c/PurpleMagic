// Package moderation defines game-owned clear history and admission controls.
package moderation

import (
	"errors"
	"net/netip"
	"strings"
	"time"
)

type Clear struct {
	Key           string    `json:"key"`
	AccountID     string    `json:"accountId"`
	CharacterID   string    `json:"characterId"`
	CharacterName string    `json:"characterName"`
	DungeonID     int64     `json:"dungeonId"`
	DungeonName   string    `json:"dungeonName"`
	Difficulty    int       `json:"difficulty"`
	StartedAt     time.Time `json:"startedAt"`
	FinishedAt    time.Time `json:"finishedAt"`
	DurationMS    int64     `json:"durationMs"`
}
type Query struct {
	Search        string    `json:"search,omitempty"`
	MaxDurationMS int64     `json:"maxDurationMs,omitempty"`
	MinHourlyPeak int64     `json:"minHourlyPeak,omitempty"`
	From          time.Time `json:"from,omitempty"`
	To            time.Time `json:"to,omitempty"`
	DungeonID     int64     `json:"dungeonId,omitempty"`
	Offset        int       `json:"offset,omitempty"`
	Limit         int       `json:"limit,omitempty"`
}

func (q *Query) Normalize(now time.Time) error {
	q.Search = strings.TrimSpace(q.Search)
	if q.Limit == 0 {
		q.Limit = 50
	}
	if q.From.IsZero() {
		q.From = now.Add(-24 * time.Hour)
	}
	if q.To.IsZero() {
		q.To = now
	}
	if q.MaxDurationMS < 0 || q.MaxDurationMS > 86400000 || q.MinHourlyPeak < 0 || q.MinHourlyPeak > 100000 || len(q.Search) > 128 || q.Limit < 1 || q.Limit > 100 || q.Offset < 0 || q.Offset > 100000 || q.DungeonID < 0 || !q.To.After(q.From) || q.To.Sub(q.From) > 31*24*time.Hour {
		return errors.New("invalid query")
	}
	return nil
}

type ClearStats struct {
	AccountID     string    `json:"accountId"`
	CharacterID   string    `json:"characterId"`
	CharacterName string    `json:"characterName"`
	DungeonID     int64     `json:"dungeonId"`
	DungeonName   string    `json:"dungeonName"`
	Difficulty    int       `json:"difficulty"`
	Count         int64     `json:"count"`
	PeakHourly    int64     `json:"peakHourly"`
	LastClearAt   time.Time `json:"lastClearAt"`
	FastestMS     int64     `json:"fastestMs"`
	AverageMS     int64     `json:"averageMs"`
}
type ClearPage struct {
	Rows    []ClearStats `json:"rows"`
	HasMore bool         `json:"hasMore"`
	From    time.Time    `json:"from"`
	To      time.Time    `json:"to"`
}
type Ban struct {
	Kind      string     `json:"kind"`
	Target    string     `json:"target"`
	Label     string     `json:"label"`
	Reason    string     `json:"reason"`
	ExpiresAt *time.Time `json:"expiresAt"`
	Revoked   bool       `json:"revoked"`
	Active    bool       `json:"active"`
	Revision  uint64     `json:"revision"`
	UpdatedAt time.Time  `json:"updatedAt"`
}
type BanChange struct {
	RequestID string     `json:"requestId"`
	Kind      string     `json:"kind"`
	Target    string     `json:"target"`
	Reason    string     `json:"reason"`
	ExpiresAt *time.Time `json:"expiresAt"`
	Revoke    bool       `json:"revoke"`
	Revision  uint64     `json:"revision"`
}

func CanonicalIP(raw string) (string, error) {
	ip, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil || ip.Zone() != "" {
		return "", errors.New("invalid IP")
	}
	return ip.Unmap().String(), nil
}
func (c *BanChange) Normalize(now time.Time) error {
	c.Target = strings.TrimSpace(c.Target)
	c.Reason = strings.TrimSpace(c.Reason)
	if len(c.RequestID) < 16 || len(c.RequestID) > 80 || c.Target == "" || len(c.Target) > 128 || len(c.Reason) == 0 || len(c.Reason) > 512 {
		return errors.New("invalid ban change")
	}
	for _, r := range c.RequestID {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return errors.New("invalid request ID")
		}
	}
	if c.Kind == "ip" {
		ip, err := CanonicalIP(c.Target)
		if err != nil {
			return err
		}
		c.Target = ip
	} else if c.Kind != "account" {
		return errors.New("invalid ban target")
	}
	if c.Revoke {
		c.ExpiresAt = nil
	} else if c.ExpiresAt != nil && (!c.ExpiresAt.After(now) || c.ExpiresAt.Sub(now) > 10*365*24*time.Hour) {
		return errors.New("invalid expiry")
	}
	return nil
}

type BanPage struct {
	Rows    []Ban `json:"rows"`
	HasMore bool  `json:"hasMore"`
}
type Account struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	LastLoginIP    string `json:"lastLoginIp"`
	RegistrationIP string `json:"registrationIp"`
	Disabled       bool   `json:"disabled"`
}
type AccountPage struct {
	Rows    []Account `json:"rows"`
	HasMore bool      `json:"hasMore"`
}
