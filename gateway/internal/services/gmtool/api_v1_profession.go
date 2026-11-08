package gmtool

import (
	"context"
	"dnf90.local/gateway/internal/modules/dnf/adminops"
	"dnf90.local/gateway/internal/modules/dnf/profession"
	"dnf90.local/gateway/internal/modules/dnf/repository"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Server) professionDefinition(ctx context.Context, c repository.CharacterRecord, job, grow int) (adminops.ProfessionDefinition, error) {
	var d adminops.ProfessionDefinition
	if job < 0 || job > 255 || grow < 0 || grow > 255 {
		return d, adminops.ErrProfessionInput
	}
	ix, resources, quests, e := s.professionManagementResources(ctx, byte(job))
	if e != nil {
		return d, e
	}
	name := ""
	for _, choice := range resources.Profiles.Choices(byte(job)) {
		if int(choice.GrowType) == grow {
			name = choice.Name
			break
		}
	}
	if name == "" {
		return d, adminops.ErrProfessionInput
	}
	oldJob, e := strconv.Atoi(c.Job)
	if e != nil || oldJob < 0 || oldJob > 255 {
		return d, adminops.ErrLevelState
	}
	target := repository.CloneCharacter(c)
	target.Job = strconv.Itoa(job)
	target.Stats["grow_type"] = int64(grow)
	growth, e := s.levelDefinition(ctx, target, c.Level)
	if e != nil {
		return d, e
	}
	if growth.PVFChecksum != ix.Checksum {
		return d, adminops.ErrProfessionDefinition
	}
	return adminops.ProfessionDefinition{PVFChecksum: ix.Checksum, ManifestHash: ix.ManifestHash, Job: job, GrowType: grow, Name: name, Growth: growth, QuestIDs: quests.ProfessionProgressionQuestIDs(job, grow), PreviousQuestIDs: quests.ProfessionProgressionQuestIDs(oldJob, int(c.Stats["grow_type"])), AllQuestIDs: quests.ProfessionDefinitionIDs(), Profiles: resources.Profiles, Skills: resources.Skills}, nil
}
func (s *Server) v1Profession(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		v1Fail(w, r, 400, "invalid_query", "此接口不接受查询参数", false)
		return
	}
	c, _, ok := s.v1ResourceCharacter(w, r)
	if !ok {
		return
	}
	g, e := s.repositories()
	if e != nil {
		s.v1Unavailable(w, r, e)
		return
	}
	u, ok := g.Character.(repository.ProfessionManagementReader)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	job, e := strconv.Atoi(c.Job)
	if e != nil || job < 0 || job > 255 {
		s.v1ProfessionResult(w, r, adminops.ProfessionReceipt{}, adminops.ErrLevelState)
		return
	}
	ix, _, quests, e := s.professionManagementResources(r.Context(), byte(job))
	if e != nil {
		s.v1Unavailable(w, r, e)
		return
	}
	v, e := u.LoadProfessionManagement(r.Context(), c.CharacterID, quests.ProfessionDefinitionIDs())
	if e != nil {
		s.v1ProfessionResult(w, r, adminops.ProfessionReceipt{}, e)
		return
	}
	if v.Growth.Character.AccountID != c.AccountID || v.Growth.Character.Stats["delete_flag"] != 0 {
		s.v1ProfessionResult(w, r, adminops.ProfessionReceipt{}, adminops.ErrAccount)
		return
	}
	if v.Growth.Character.Job != c.Job {
		v1Fail(w, r, 409, "revision_conflict", "职业已变更，请刷新", true)
		return
	}
	b, e := adminops.ProfessionValues(v)
	if e != nil {
		s.v1ProfessionResult(w, r, adminops.ProfessionReceipt{}, e)
		return
	}
	jobs := []map[string]any{}
	for _, j := range ix.Manifest.Jobs {
		jobs = append(jobs, map[string]any{"job": j.Job, "name": j.Name, "choices": j.Choices})
	}
	v1Reply(w, r, 200, v1Envelope{Data: map[string]any{"accountId": c.AccountID, "characterId": c.CharacterID, "balance": b, "pvfChecksum": ix.Checksum, "jobs": jobs, "revision": adminops.ProfessionRevision(v)}})
}
func (s *Server) v1ProfessionOperation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AccountID          string `json:"accountId"`
		TargetJob          int    `json:"targetJob"`
		TargetGrowType     int    `json:"targetGrowType"`
		Revision           string `json:"revision"`
		PVFChecksum        string `json:"pvfChecksum"`
		DefinitionRevision string `json:"definitionRevision"`
		Reason             string `json:"reason"`
		PlanToken          string `json:"planToken"`
	}
	if !v1Decode(w, r, &body) {
		return
	}
	commit := !strings.HasSuffix(r.URL.Path, "/preview")
	key := ""
	if commit {
		var ok bool
		key, ok = v1Idempotency(w, r)
		if !ok {
			return
		}
	}
	c, _, ok := s.v1ResourceCharacter(w, r)
	if !ok {
		return
	}
	if c.AccountID != body.AccountID {
		v1Fail(w, r, 409, "owner_conflict", "角色所属账号已变更，请重新打开角色", false)
		return
	}
	g, e := s.repositories()
	if e != nil {
		s.v1Unavailable(w, r, e)
		return
	}
	u, ok := g.Character.(repository.ProfessionManagementUnitOfWork)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	actor := "管理员"
	if s.cfg.LoginKeySHA256 != "" {
		actor = "管理凭据 " + s.cfg.LoginKeySHA256[:min(12, len(s.cfg.LoginKeySHA256))]
	}
	command := adminops.ProfessionCommand{AccountID: c.AccountID, CharacterID: c.CharacterID, TargetJob: body.TargetJob, TargetGrowType: body.TargetGrowType, Revision: body.Revision, PVFChecksum: body.PVFChecksum, DefinitionRevision: body.DefinitionRevision, Reason: body.Reason, Actor: actor, PlanToken: body.PlanToken, Key: key}
	if commit {
		old, found, e := adminops.LookupProfessionOperation(r.Context(), u, command)
		if found || e != nil {
			s.v1ProfessionResult(w, r, old, e)
			return
		}
		identity := command
		identity.PlanToken = ""
		raw, e := s.v1CursorAfter(command.PlanToken, "profession-plan", adminops.ProfessionIdentity(identity))
		at, parseErr := strconv.ParseInt(raw, 10, 64)
		age := time.Since(time.Unix(at, 0))
		if e != nil || parseErr != nil || age < 0 || age > 10*time.Minute {
			v1Fail(w, r, 409, "plan_conflict", "职业预览已过期或内容已变更，请重新预览", false)
			return
		}
	} else {
		command.PlanToken = ""
	}
	result, e := adminops.RunProfessionOperation(r.Context(), u, command, commit, s.professionDefinition, s.v1CharacterEditCheck(command.AccountID))
	if e == nil && !commit {
		command.DefinitionRevision = result.DefinitionRevision
		v1Reply(w, r, 200, v1Envelope{Data: map[string]any{"receipt": result, "definitionRevision": result.DefinitionRevision, "planToken": s.v1CursorToken("profession-plan", adminops.ProfessionIdentity(command), strconv.FormatInt(time.Now().Unix(), 10))}})
		return
	}
	s.v1ProfessionResult(w, r, result, e)
}
func (s *Server) v1ProfessionResult(w http.ResponseWriter, r *http.Request, result adminops.ProfessionReceipt, e error) {
	switch {
	case e == nil:
		v1Reply(w, r, 200, v1Envelope{Data: result})
	case errors.Is(e, adminops.ErrProfessionDefinition):
		v1Fail(w, r, 409, "pvf_conflict", "职业定义已变更，请刷新后重新预览", false)
	case errors.Is(e, adminops.ErrProfessionInput), errors.Is(e, profession.ErrTransitionInvalid), errors.Is(e, profession.ErrTransitionOutOfStep):
		v1Fail(w, r, 422, "invalid_profession", "请选择当前等级可用的职业与觉醒阶段", false)
	default:
		s.v1LevelResult(w, r, adminops.LevelReceipt{}, e)
	}
}
func (s *Server) v1ProfessionLog(w http.ResponseWriter, r *http.Request) {
	q, e := v1ParseQuery(r)
	if e != nil || q.Q != "" {
		v1Fail(w, r, 400, "invalid_query", "职业记录查询参数无效", false)
		return
	}
	c, _, ok := s.v1ResourceCharacter(w, r)
	if !ok {
		return
	}
	g, e := s.repositories()
	if e != nil {
		s.v1Unavailable(w, r, e)
		return
	}
	u, ok := g.Character.(repository.ProfessionManagementUnitOfWork)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	filter := v1Filter(q, c.CharacterID+"\x00"+c.AccountID)
	raw, e := s.v1CursorAfter(q.Cursor, "profession-log", filter)
	var before int64
	if e == nil && raw != "" {
		before, e = strconv.ParseInt(raw, 10, 64)
	}
	if e != nil || before < 0 {
		v1Fail(w, r, 400, "invalid_cursor", "职业记录分页条件已变更", false)
		return
	}
	rows, e := u.ProfessionManagementLog(r.Context(), c.CharacterID, before, q.Limit+1)
	if e != nil {
		s.v1Unavailable(w, r, e)
		return
	}
	page := v1Page{Limit: q.Limit}
	if len(rows) > q.Limit {
		rows = rows[:q.Limit]
		page.NextCursor = s.v1CursorToken("profession-log", filter, strconv.FormatInt(rows[len(rows)-1].Sequence, 10))
	}
	items := []adminops.ProfessionReceipt{}
	for _, row := range rows {
		v, e := adminops.DecodeProfessionReceipt(row.Payload)
		if e != nil {
			s.v1Unavailable(w, r, e)
			return
		}
		items = append(items, v)
	}
	v1Reply(w, r, 200, v1Envelope{Data: items, Page: &page})
}
