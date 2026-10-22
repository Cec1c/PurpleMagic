package gmtool

import (
	"net/http"
	"strconv"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/repository"
	"dnf90.local/gateway/internal/modules/dnf/testbenefits"
)

type v1MailInputError struct {
	status        int
	code, message string
}

func (e *v1MailInputError) Error() string { return e.message }
func (e *v1MailInputError) DiagnosticFields() []any {
	return []any{"operation_code", e.code, "operation_status", e.status}
}

func (s *Server) v1MailGrantOptions(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("itemId"), 10, 31)
	if err != nil || id == 0 || r.URL.RawQuery != "" {
		v1Fail(w, r, 400, "invalid_item", "物品编号无效", false)
		return
	}
	if !s.v1RequireItemIndex(w, r) {
		return
	}
	entry, found := s.items.lookup(uint32(id))
	if !found || entry.Invalid {
		v1Fail(w, r, 404, "item_not_found", "当前物品库没有可用的此物品", false)
		return
	}
	checksum := s.v1PVFChecksum()
	if checksum == "" {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	payload := map[string]any{
		"itemId": strconv.FormatUint(id, 10), "name": entry.Name, "kind": entry.Kind, "pvfChecksum": checksum,
		"equipment":  map[string]any{"maxUpgradeLevel": gmMailReinforceMax},
		"expiration": map[string]any{"maxDays": gmMailExpireDaysMax, "defaultExpireTime": entry.AbsoluteExpireUnix, "usablePeriodDays": entry.UsablePeriodDays},
	}
	// Versioned clients use the validated mail projection directly. The legacy
	// page's environment flag controls only its own compatibility route.
	s.gmGrantOptionsCapability(entry, payload)
	v1Reply(w, r, 200, v1Envelope{Data: payload})
}

func (s *Server) v1ProjectMailDraft(d testbenefits.MailDraft, at time.Time) (testbenefits.Job, error) {
	if s.deps.Catalog != nil && d.ItemID != "" {
		var v testbenefits.Job
		d.Recipients = nil
		d.AccountIDs = nil
		e := s.deps.Catalog.background(catalogRequest{Operation: "mail", Draft: d, At: at}, &v)
		return v, e
	}
	j, _, err := s.v1ResolveReward(v1RewardInput{Title: d.Title, Body: d.Body, Gold: d.Gold, ItemID: d.ItemID, Count: d.Count}, testbenefits.ScopeCharacter)
	if err != nil {
		return j, err
	}
	if len(j.Attachments) == 0 {
		if d.Options != (testbenefits.MailOptions{}) || d.PVFChecksum != "" {
			return j, &v1MailInputError{422, "invalid_options", "物品参数需要选择附件"}
		}
		return j, nil
	}
	if d.PVFChecksum == "" || d.PVFChecksum != s.v1PVFChecksum() {
		return j, &v1MailInputError{409, "pvf_conflict", "PVF 已变更，请重新读取发放参数"}
	}
	entry, found := s.items.lookup(uint32(j.Attachments[0].ItemID))
	if !found {
		return j, &v1MailInputError{422, "invalid_item", "物品不存在"}
	}
	o := gmMailGrantOptions{ExpireDays: d.Options.ExpireDays, Reinforce: d.Options.Reinforce, AmplifyType: d.Options.AmplifyType, EquipmentQuality: d.Options.EquipmentQuality, ForgingLevel: d.Options.ForgingLevel}
	if rejection := s.validateGMMailGrantOptions(entry, int(d.Count), o); rejection != nil {
		return j, &v1MailInputError{422, "invalid_options", rejection.message}
	}
	p, err := s.applyGMMailGrantOptions(entry, j.Attachments[0].Extra, o, at)
	if err != nil {
		return j, err
	}
	j.Attachments[0].Extra, j.Attachments[0].ExpireAt = p.extra, p.expireAt
	return j, nil
}

func v1MailAttachmentDTO(a repository.MailAttachment) map[string]any {
	options := map[string]string{}
	for field, key := range map[string]string{"durability": "durability", "maxDurability": "max_durability", "reinforce": "reinforce", "amplifyType": "amplify_type", "amplifyValue": "amplify_value", "qualitySeed": "quality_seed", "forgingLevel": "byte_57", "expireDays": "gm_expire_days"} {
		if value, ok := a.Extra[key]; ok {
			options[field] = value
		}
	}
	return map[string]any{"itemId": strconv.FormatInt(a.ItemID, 10), "count": a.Count, "options": options, "expiresAt": v1Time(a.ExpireAt)}
}
