package gmtool

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/testbenefits"
)

type benefitRedeliveryInput struct {
	Revision uint64    `json:"revision"`
	Through  time.Time `json:"through"`
	Cursor   string    `json:"cursor"`
}
type benefitRedeliveryResult struct {
	Next     string                     `json:"next"`
	Checked  int                        `json:"checked"`
	Received int                        `json:"received"`
	Skipped  int                        `json:"skipped"`
	Failed   int                        `json:"failed"`
	Failures []benefitRedeliveryFailure `json:"failures,omitempty"`
	Stop     string                     `json:"stop,omitempty"`
}
type benefitRedeliveryFailure struct {
	AccountID   string `json:"accountId"`
	CharacterID string `json:"characterId"`
	Message     string `json:"message"`
}

// Private worker endpoint: one recipient, at most five account reads, and a
// bounded transaction. No all-server preview and no new reward identity.
func (s *Server) redeliverBenefitPage(w http.ResponseWriter, r *http.Request) {
	var in benefitRedeliveryInput
	if !v1Decode(w, r, &in) {
		return
	}
	if !s.v1OperationReady(w, r) {
		return
	}
	if len(in.Cursor) > 256 || in.Through.IsZero() || in.Through.After(time.Now().Add(time.Minute)) {
		v1Fail(w, r, 400, "invalid_batch", "补发参数无效", false)
		return
	}
	ctx, stop := context.WithTimeout(r.Context(), 7*time.Second)
	defer stop()
	c, err := s.deps.Benefits.Campaign(ctx, r.PathValue("id"))
	if err != nil {
		s.v1OperationError(w, r, err)
		return
	}
	out := benefitRedeliveryResult{}
	switch {
	case !c.Enabled:
		out.Stop = "规则已暂停，请启用后重新补发"
	case c.Revision != in.Revision:
		out.Stop = "规则已更新，请核对后重新补发"
	case c.Reward.PVFChecksum != "" && c.Reward.PVFChecksum != s.v1PVFChecksum():
		out.Stop = "PVF 已变化，请先核对福利规则"
	}
	if out.Stop == "" {
		group, e := s.v1OperationGroup()
		if e != nil {
			s.v1OperationError(w, r, e)
			return
		}
		rows, next, e := s.deps.Benefits.CampaignRecipientPageThrough(ctx, group, c, in.Cursor, 1, in.Through)
		if e != nil {
			s.v1OperationError(w, r, e)
			return
		}
		out.Next = next
		for _, rec := range rows {
			out.Checked++
			if !rec.Eligible || rec.CreatedAt.After(in.Through) {
				out.Skipped++
				continue
			}
			if rec.MailID != "" {
				out.Received++
				continue
			}
			delivery, cancel := context.WithTimeout(ctx, 4*time.Second)
			_, e = s.deps.Benefits.RedeliverCampaignRecipient(delivery, group, c.ID, in.Revision, rec.Recipient, s.v1ProjectCampaignDelivery)
			cancel()
			if errors.Is(e, testbenefits.ErrRevision) || errors.Is(e, testbenefits.ErrCampaignDisabled) || errors.Is(e, testbenefits.ErrPVFReview) {
				out.Stop = "规则状态或 PVF 已变化，请核对后重新补发"
				break
			}
			if e == nil {
				out.Received++
				continue
			}
			out.Failed++
			out.Failures = append(out.Failures, benefitRedeliveryFailure{rec.AccountID, rec.CharacterID, v1DeliveryFailure(e.Error()).Message})
			if s.cfg.Logger != nil {
				s.cfg.Logger("benefit-redelivery-failed", "campaign_id", c.ID, "character_id", rec.CharacterID, "cause", e.Error())
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
