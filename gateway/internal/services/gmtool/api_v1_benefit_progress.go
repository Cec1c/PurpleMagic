package gmtool

import (
	"dnf90.local/gateway/internal/diagnostics"
	"dnf90.local/gateway/internal/modules/dnf/testbenefits"
	"net/http"
)

func (s *Server) v1CampaignRecipients(w http.ResponseWriter, r *http.Request) {
	resource := "benefit-recipients:" + r.PathValue("id")
	q, after, ok := s.v1OperationPage(w, r, resource)
	if !ok {
		return
	}
	diagnostics.Field(r.Context(), "campaign_id", r.PathValue("id"))
	ruleDone := diagnostics.Start(r.Context(), "benefits.rule")
	c, err := s.deps.Benefits.Campaign(r.Context(), r.PathValue("id"))
	ruleDone(err)
	if err != nil {
		s.v1OperationError(w, r, err)
		return
	}
	group, err := s.v1OperationGroup()
	if err != nil {
		s.v1OperationError(w, r, err)
		return
	}
	pageDone := diagnostics.Start(r.Context(), "benefits.recipients")
	rows, next, err := s.deps.Benefits.CampaignRecipientPage(r.Context(), group, c, after, q.Limit)
	pageDone(err)
	if err != nil {
		s.v1OperationError(w, r, err)
		return
	}
	out := []map[string]any{}
	assemblyDone := diagnostics.Start(r.Context(), "benefits.assembly")
	for _, r := range rows {
		state := "pending"
		if !r.Eligible {
			state = "ineligible"
		}
		if r.MailID != "" {
			state = "sent"
		} else if r.Error != "" {
			state = "failed"
		}
		var delivered any
		if r.Delivery != nil {
			delivered = map[string]any{"grantedAt": r.Delivery.GrantedAt, "pvfChecksum": r.Delivery.PVFChecksum, "before": r.Delivery.Before, "after": r.Delivery.After, "reward": v1RewardDTO(testbenefits.Job{Cera: r.Delivery.Cera, Attachments: r.Delivery.Attachments})}
		}
		out = append(out, map[string]any{"accountId": r.AccountID, "characterId": r.CharacterID, "name": r.Name, "mailId": r.MailID, "deliveredCharacterId": r.DeliveredCharacterID, "state": state, "delivery": delivered, "failure": v1DeliveryFailure(r.Error)})
	}
	if next != "" {
		next = s.v1CursorToken(resource, v1Filter(q, ""), next)
	}
	assemblyDone(nil)
	v1Reply(w, r, 200, v1Envelope{Data: out, Page: &v1Page{Limit: q.Limit, NextCursor: next}})
}
