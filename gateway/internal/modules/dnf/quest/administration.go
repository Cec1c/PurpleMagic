package quest

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/repository"
)

var ErrAdministrationAction = errors.New("invalid quest administration action")
var ErrAdministrationProfession = errors.New("profession quest requires profession operation")
var ErrAdministrationSettlement = errors.New("quest has an existing reward settlement")

// AdministrationSlotExpansion resolves unlocks from the approved PVF, including
// quests previously marked completed without their slot reward by older GM code.
// It is detached; the caller commits these bits with the quest and its receipt.
func AdministrationSlotExpansion(catalog *Catalog, character repository.CharacterRecord, action string, ids []int64) (byte, error) {
	if action != "complete" {
		return 0, nil
	}
	job, err := strconv.Atoi(character.Job)
	if err != nil {
		return 0, ErrQuestNotAcceptable
	}
	eligibility := CharacterEligibility{Level: character.Level, Job: job, GrowType: int(character.Stats["grow_type"])}
	var bits byte
	for _, id := range ids {
		d, found := catalog.Find(id)
		if !found {
			return 0, &AdministrationError{id, ErrQuestDefinitionMissing}
		}
		if normalizeQuestTag(d.RewardType) != "slot expansion" {
			continue
		}
		plan, err := catalog.PlanFinishReward(eligibility, id, 0, false)
		if err != nil {
			return 0, &AdministrationError{id, err}
		}
		bits |= plan.SlotExpansionBit
	}
	return bits, nil
}

type AdministrationError struct {
	QuestID int64
	Err     error
}

func (e *AdministrationError) Error() string { return fmt.Sprintf("quest %d: %v", e.QuestID, e.Err) }
func (e *AdministrationError) Unwrap() error { return e.Err }

// PlanAdministration applies established quest rules to a detached snapshot.
// Accept/give-up use the same Owner as gameplay; no SQL or player rewards run here.
func PlanAdministration(ctx context.Context, catalog *Catalog, character repository.CharacterRecord, record repository.QuestRecord, action string, ids []int64) (repository.QuestRecord, error) {
	numeric, err := strconv.ParseUint(character.CharacterID, 10, 16)
	if err != nil || numeric == 0 || strconv.FormatUint(numeric, 10) != character.CharacterID {
		return repository.QuestRecord{}, ErrCharacterRequired
	}
	job, err := strconv.Atoi(character.Job)
	if err != nil || job < 0 || job > 255 || character.Stats["grow_type"] < 0 || character.Stats["grow_type"] > 255 {
		return repository.QuestRecord{}, ErrQuestNotAcceptable
	}
	eligibility := CharacterEligibility{Level: character.Level, Job: job, GrowType: int(character.Stats["grow_type"])}
	working := &administrationQuests{record: repository.CloneQuest(record)}
	working.record.CharacterID = character.CharacterID
	owner, err := NewOwner(repository.Group{Character: administrationCharacter{record: character}, Quest: working})
	if err != nil {
		return repository.QuestRecord{}, err
	}
	resetFamilies := map[int64]bool{}
	if action == "reset" {
		for _, id := range ids {
			state, known := questStateFor(record, id)
			if !known || !isCompletedQuestStatus(state.Status) {
				return repository.QuestRecord{}, &AdministrationError{id, ErrQuestNotActive}
			}
		}
	}
	for _, id := range ids {
		if err = ctx.Err(); err != nil {
			return repository.QuestRecord{}, err
		}
		d, found := catalog.Find(id)
		if !found {
			return repository.QuestRecord{}, &AdministrationError{id, ErrQuestDefinitionMissing}
		}
		if id <= 0 || id > 65535 {
			return repository.QuestRecord{}, ErrQuestIDRequired
		}
		if action == "reset" {
			var family int64
			if members, ok := catalog.ManagedLinkedQuestChainFamily(d.MainQuestID); ok {
				for _, member := range members {
					if member == id {
						family = d.MainQuestID
						break
					}
				}
			}
			if _, isParent := catalog.ManagedLinkedQuestChainFamily(id); isParent {
				family = id
			}
			if family > 0 {
				if resetFamilies[family] {
					continue
				}
				resetFamilies[family] = true
			}
		}
		cmd := Command{AccountID: character.AccountID, SelectedCharacterID: uint16(numeric), QuestID: uint16(id)}
		switch action {
		case "accept":
			_, err = owner.ApplyAccept(ctx, catalog, eligibility, cmd)
		case "give-up":
			_, err = owner.ApplyGiveUp(ctx, catalog, cmd)
		case "ready", "complete", "reset":
			err = planAdministrativeState(catalog, eligibility, &working.record, d, action)
		default:
			err = ErrAdministrationAction
		}
		if err != nil {
			return repository.QuestRecord{}, &AdministrationError{id, err}
		}
	}
	return working.record, nil
}

func planAdministrativeState(catalog *Catalog, eligibility CharacterEligibility, record *repository.QuestRecord, d Definition, action string) error {
	state, known := questStateFor(*record, d.ID)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if action == "reset" {
		if !known || !isCompletedQuestStatus(state.Status) {
			return ErrQuestNotActive
		}
		if d.MainQuestID > 0 {
			plan, e := catalog.PlanLinkedQuestChainReset(eligibility, *record, d.ID, now)
			if e != nil {
				return e
			}
			*record = plan.Record
			return nil
		}
		if questClearDefinition(d) && len(catalog.linkedQuestChainUnresolvedChildIDs(d)) > 0 {
			return ErrQuestChainChildUnresolved
		}
		if _, family := catalog.ManagedLinkedQuestChainFamily(d.ID); family {
			plan, e := catalog.PlanLinkedQuestChainResetFromParent(eligibility, *record, d.ID, now)
			if e != nil {
				return e
			}
			*record = plan.Record
			return nil
		}
		delete(record.States, d.ID)
		delete(record.Progress, d.ID)
		record.UpdatedAt = now
		return nil
	}
	if action == "ready" {
		if !known || !isActiveQuestStatus(state.Status) {
			return ErrQuestNotActive
		}
		if state.Extra["reward_state"] != "" || state.Extra["settlement_receipt_v1"] != "" {
			return ErrAdministrationSettlement
		}
		state.ProgressValue = 0
		if state.Extra == nil {
			state.Extra = map[string]string{}
		}
		state.Extra["gm_marked_ready"] = "true"
	} else {
		reward := strings.Trim(strings.ToLower(strings.TrimSpace(d.RewardType)), "[] `")
		if reward == "grow type" || reward == "awakening type" || d.JobChangeQuest > 0 {
			return ErrAdministrationProfession
		}
		if known && isCompletedQuestStatus(state.Status) {
			return nil
		}
		// Preserve existing completion proofs and mark the row settled, matching
		// the domain's no-reward completion contract used by quest-clear coupons.
		state.Status = "completed"
		state.ProgressValue = 0
		state.RewardSelectIndex = 0
		if state.Extra == nil {
			state.Extra = map[string]string{}
		}
		state.Extra["reward_state"] = finishRewardGranted
		state.Extra["gm_force_completed"] = "true"
		state.Extra["gm_rewards_granted"] = "false"
		state.Extra["pvf_path"] = d.Path
	}
	state.UpdatedAt = now
	if record.States == nil {
		record.States = map[int64]repository.QuestState{}
	}
	record.States[d.ID] = state
	delete(record.Progress, d.ID)
	record.UpdatedAt = now
	return nil
}

type administrationQuests struct{ record repository.QuestRecord }

func (s *administrationQuests) Load(ctx context.Context, id string) (repository.QuestRecord, bool, error) {
	if id != s.record.CharacterID {
		return repository.QuestRecord{}, false, ErrCharacterNotFound
	}
	return repository.CloneQuest(s.record), true, ctx.Err()
}
func (s *administrationQuests) Save(ctx context.Context, r repository.QuestRecord) error {
	if r.CharacterID != s.record.CharacterID {
		return ErrCharacterNotFound
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.record = repository.CloneQuest(r)
	return nil
}

type administrationCharacter struct{ record repository.CharacterRecord }

func (s administrationCharacter) Load(ctx context.Context, id string) (repository.CharacterRecord, bool, error) {
	if id != s.record.CharacterID {
		return repository.CharacterRecord{}, false, ErrCharacterNotFound
	}
	return repository.CloneCharacter(s.record), true, ctx.Err()
}
func (s administrationCharacter) Save(context.Context, repository.CharacterRecord) error {
	return ErrAdministrationAction
}
func (s administrationCharacter) ListByAccount(context.Context, string, int) ([]repository.CharacterRecord, error) {
	return nil, ErrAdministrationAction
}
func (s administrationCharacter) FindIDByName(context.Context, string) (string, bool, error) {
	return "", false, ErrAdministrationAction
}
func (s administrationCharacter) NextNumericID(context.Context) (int, error) {
	return 0, ErrAdministrationAction
}
