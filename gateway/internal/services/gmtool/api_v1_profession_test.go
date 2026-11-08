package gmtool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	"dnf90.local/gateway/internal/modules/dnf/adminops"
	"dnf90.local/gateway/internal/modules/dnf/progression"
	dnfpvf "dnf90.local/gateway/internal/modules/dnf/pvf"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

type professionFailStore struct {
	repository.ProfessionManagementRepository
}

func (s professionFailStore) SaveProfessionManagement(ctx context.Context, v repository.ProfessionManagementState, ids []int64) error {
	if e := s.ProfessionManagementRepository.SaveProfessionManagement(ctx, v, ids); e != nil {
		return e
	}
	return errors.New("injected profession save failure")
}

type professionFailUOW struct {
	repository.ProfessionManagementUnitOfWork
	save bool
}

func (s professionFailUOW) WithinProfessionManagement(ctx context.Context, a, id string, fn func(repository.ProfessionManagementRepository, repository.ManagementReceiptStore) error) error {
	return s.ProfessionManagementUnitOfWork.WithinProfessionManagement(ctx, a, id, func(r repository.ProfessionManagementRepository, receipts repository.ManagementReceiptStore) error {
		if s.save {
			r = professionFailStore{r}
		} else {
			receipts = failingReceipts{receipts}
		}
		return fn(r, receipts)
	})
}
func TestV1ProfessionMySQL(t *testing.T) {
	for _, shards := range []int{1, 2} {
		t.Run(fmt.Sprint(shards), func(t *testing.T) {
			ctx := context.Background()
			s, _, open := currencyFixture(t, shards)
			g := open()
			check := func(e error) {
				t.Helper()
				if e != nil {
					t.Fatal(e)
				}
			}
			docs := professionIndexFixture("职业").readonlyTestSource
			level := levelTestSource()
			for _, path := range []string{progression.ExperienceTablePath, progression.SkillPointTablePath} {
				docs[path] = level.readonlyTestSource[path]
			}
			for _, path := range []string{"character/zero.chr", "character/two.chr"} {
				docs[path] = strings.Replace(docs[path], "[initial value]", "[initial value]\n[HP MAX] 100\n[light resistance] -20", 1)
			}
			source := newItemCacheTestSource(docs)
			summary := filepath.Join(t.TempDir(), "items.json")
			_, e := BuildProfessionIndex(ctx, source, summary)
			check(e)
			s.pvf = newPVFCache()
			s.deps.PVF = func() (dnfpvf.Source, error) { return source, nil }
			s.items = newItemIndex(summary, s, nil)
			for i, id := range []string{"10", "11", "20", "21"} {
				a := "wallet:1"
				if i > 1 {
					a = "wallet:2"
				}
				check(g.Character.Save(ctx, repository.CharacterRecord{CharacterID: id, AccountID: a, Slot: i, Name: "职业" + id, Job: "2", Level: 90, Stats: map[string]int64{"grow_type": 1, "exp": 888888, "bonus_sp": 40, "bonus_tp": 3, "gold": 12345, "keep": 42, "premium_crystal_selection": -1}}))
				if id != "21" {
					check(g.Skill.Save(ctx, repository.SkillRecord{CharacterID: id, Points: repository.SkillPointState{TotalSP: 9040, RemainingSP: 8040, TotalTP: 93, RemainingTP: 83, SyncedLevel: 90}, Skills: map[int64]repository.SkillState{1: {Level: 2, Enabled: true}, 2: {Level: 1, Enabled: true}, 99: {Level: 7, Enabled: true}}, Layouts: map[int]repository.SkillLayout{0: {0: 1}, 1: {1: 99}}, Cooldowns: map[int64]time.Time{99: time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)}}))
				}
				check(g.Quest.Save(ctx, repository.QuestRecord{CharacterID: id, States: map[int64]repository.QuestState{1: {Status: "active", ProgressValue: 9, Extra: map[string]string{"story": "keep"}}, 2: {Status: "active", ProgressValue: 7, Multiplier: 3, RewardSelectIndex: 5, TriggerType: 4, Extra: map[string]string{"reward_state": "granted", "receipt": "keep"}}, 4: {Status: "completed", Extra: map[string]string{"expert": "keep"}}}, Progress: map[int64]repository.QuestState{2: {Status: "active", ProgressValue: 8, Extra: map[string]string{"reward_state": "pending"}}}}))
			}
			u := g.Character.(repository.ProfessionManagementUnitOfWork)
			read := func(id string) repository.ProfessionManagementState {
				_, _, q, e := s.professionManagementResources(ctx, 2)
				check(e)
				v, e := open().Character.(repository.ProfessionManagementReader).LoadProfessionManagement(ctx, id, q.ProfessionDefinitionIDs())
				check(e)
				return v
			}
			full := func(id string) (repository.CharacterRecord, repository.SkillRecord, repository.QuestRecord) {
				c, _, e := g.Character.Load(ctx, id)
				check(e)
				k, _, e := g.Skill.Load(ctx, id)
				check(e)
				q, _, e := g.Quest.Load(ctx, id)
				check(e)
				return c, k, q
			}
			c0, k0, q0 := full("10")
			otherC, otherK, otherQ := full("11")
			offline := func(context.Context, string) error { return nil }
			prepare := func(id string, job, grow int) adminops.ProfessionCommand {
				v := read(id)
				cmd := adminops.ProfessionCommand{AccountID: v.Growth.Character.AccountID, CharacterID: id, TargetJob: job, TargetGrowType: grow, Revision: adminops.ProfessionRevision(v), PVFChecksum: source.checksum, Reason: "职业调整", Actor: "管理员", Key: fmt.Sprintf("profession-%s-%d", id, time.Now().UnixNano())}
				p, e := adminops.RunProfessionOperation(ctx, u, cmd, false, s.professionDefinition, nil)
				check(e)
				cmd.DefinitionRevision = p.DefinitionRevision
				return cmd
			}
			cmd := prepare("10", 2, 17)
			unchanged := func() {
				t.Helper()
				c, k, q := full("10")
				if !reflect.DeepEqual(c, c0) || !reflect.DeepEqual(k, k0) || !reflect.DeepEqual(q, q0) {
					t.Fatal("failed transaction changed data")
				}
				log, e := u.ProfessionManagementLog(ctx, "10", 0, 100)
				check(e)
				if len(log) != 0 {
					t.Fatal("unexpected receipt")
				}
			}
			unchanged()
			for _, mode := range []string{"save", "receipt", "online", "late-online", "unavailable"} {
				worker := u
				off := offline
				switch mode {
				case "save":
					worker = professionFailUOW{u, true}
				case "receipt":
					worker = professionFailUOW{u, false}
				case "online":
					off = func(context.Context, string) error { return adminops.ErrCharacterOnline }
				case "unavailable":
					off = nil
				case "late-online":
					calls := 0
					off = func(context.Context, string) error {
						calls++
						if calls == 2 {
							return adminops.ErrCharacterOnline
						}
						return nil
					}
				}
				if _, e = adminops.RunProfessionOperation(ctx, worker, cmd, true, s.professionDefinition, off); e == nil {
					t.Fatal(mode)
				}
				unchanged()
			}
			var wg sync.WaitGroup
			errs := make(chan error, 6)
			for range 6 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, e := adminops.RunProfessionOperation(ctx, u, cmd, true, s.professionDefinition, offline)
					errs <- e
				}()
			}
			wg.Wait()
			close(errs)
			for e := range errs {
				check(e)
			}
			c, k, q := full("10")
			if c.Job != "2" || c.Stats["grow_type"] != 17 || c.Stats["gold"] != 12345 || c.Stats["exp"] != 888888 || c.Stats["keep"] != 42 || c.Stats["stat_light_resistance"] != -20 || c.Stats["gm_level_sequence_v1"] != 1 {
				t.Fatal(c)
			}
			if k.Points != k0.Points || k.Skills[99].Level != 7 || k.Skills[3].Level != 1 || !reflect.DeepEqual(k.Layouts[1], k0.Layouts[1]) || !reflect.DeepEqual(k.Cooldowns, k0.Cooldowns) {
				t.Fatal(k)
			}
			if q.States[2].Status != "completed" || q.Progress[2].Status != "completed" || q.States[3].Status != "completed" || q.States[3].Extra["gm_override"] != "gm_set_grow_type" || !reflect.DeepEqual(q.States[1], q0.States[1]) || !reflect.DeepEqual(q.States[4], q0.States[4]) || !reflect.DeepEqual(q.States[2].Extra, q0.States[2].Extra) || q.States[2].Multiplier != 3 || q.States[2].RewardSelectIndex != 5 || q.States[2].TriggerType != 4 {
				t.Fatal(q)
			}
			oc, ok, oq := full("11")
			if !reflect.DeepEqual(oc, otherC) || !reflect.DeepEqual(ok, otherK) || !reflect.DeepEqual(oq, otherQ) {
				t.Fatal("other character changed")
			}
			replay, e := adminops.RunProfessionOperation(ctx, open().Character.(repository.ProfessionManagementUnitOfWork), cmd, true, nil, nil)
			check(e)
			if replay.After.GrowType != 17 {
				t.Fatal(replay)
			}
			bad := cmd
			bad.Reason = "不同原因"
			if _, e = adminops.RunProfessionOperation(ctx, u, bad, true, nil, nil); !errors.Is(e, adminops.ErrIdempotency) {
				t.Fatal(e)
			}
			bad = cmd
			bad.Key += "-stale"
			if _, e = adminops.RunProfessionOperation(ctx, u, bad, true, s.professionDefinition, offline); !errors.Is(e, adminops.ErrConflict) {
				t.Fatal(e)
			}

			// A lower PVF baseline can be refunded by a reset, while a forward
			// awakening must preserve its existing spent amount.
			reduced := func(ctx context.Context, c repository.CharacterRecord, j, g int) (adminops.ProfessionDefinition, error) {
				d, e := s.professionDefinition(ctx, c, j, g)
				d.Growth.SP = 100
				d.Growth.TP = 1
				return d, e
			}
			reducedCmd := cmd
			reducedCmd.TargetGrowType = 1
			reducedCmd.Revision = adminops.ProfessionRevision(read("10"))
			reducedCmd.DefinitionRevision = ""
			reducedPlan, e := adminops.RunProfessionOperation(ctx, u, reducedCmd, false, reduced, nil)
			check(e)
			if reducedPlan.After.Growth.Points.RemainingSP != 140 || reducedPlan.After.Growth.Points.RemainingTP != 4 {
				t.Fatal("reset refused reduced PVF baseline", reducedPlan.After)
			}
			down := prepare("10", 2, 1)
			result, e := adminops.RunProfessionOperation(ctx, u, down, true, s.professionDefinition, offline)
			check(e)
			if !result.ResetSkills || !reflect.DeepEqual(result.RetainedQuestIDs, []int64{3}) || result.After.Growth.Points.RemainingSP != 9040 {
				t.Fatal(result)
			}
			_, k, q = full("10")
			if k.Skills[3].Level != 0 || k.Skills[99].Level != 0 || len(k.Layouts) != 1 || len(k.Cooldowns) != 0 || q.States[3].Status != "completed" {
				t.Fatal(k, q)
			}
			// A real game progression transaction must finish before the GM reads its revision.
			cmdWait := prepare("11", 2, 17)
			locked := make(chan struct{})
			release := make(chan struct{})
			gameDone := make(chan error, 1)
			go func() {
				gameDone <- g.CharacterProgression.WithinCharacterProgression(ctx, "11", func(chars repository.CharacterRepository, skills repository.SkillRepository) error {
					c, _, e := chars.Load(ctx, "11")
					if e != nil {
						return e
					}
					if _, _, e = skills.Load(ctx, "11"); e != nil {
						return e
					}
					close(locked)
					<-release
					c.Stats["exp"]++
					return repository.SaveCharacterFields(ctx, chars, c, repository.CharacterFieldStats)
				})
			}()
			select {
			case <-locked:
			case e := <-gameDone:
				t.Fatal(e)
			case <-time.After(5 * time.Second):
				t.Fatal("game lock timeout")
			}
			done := make(chan error, 1)
			go func() {
				_, e := adminops.RunProfessionOperation(ctx, u, cmdWait, true, s.professionDefinition, offline)
				done <- e
			}()
			select {
			case e := <-done:
				close(release)
				t.Fatal("did not wait", e)
			case <-time.After(100 * time.Millisecond):
			}
			close(release)
			check(<-gameDone)
			if e = <-done; !errors.Is(e, adminops.ErrConflict) {
				t.Fatal("game conflict", e)
			}

			t.Run("legacy-mirror", func(t *testing.T) {
				var database string
				check(s.db.QueryRow("SELECT DATABASE()").Scan(&database))
				for i := 0; i < shards; i++ {
					db := fmt.Sprintf("`%s%d`", strings.TrimSuffix(database, "0"), i)
					_, e := s.db.Exec("INSERT INTO " + db + ".dnf_character_stats(character_id,stat_key,stat_value) SELECT character_id,'grow_type',0 FROM " + db + ".dnf_characters WHERE character_id='11'")
					check(e)
				}
				if read("11").Growth.Character.Stats["grow_type"] != 1 {
					t.Fatal("default used shadow")
				}
				t.Setenv("DNFBRIDGE_CHARACTER_MIRROR_STATS_AUTHORITATIVE", "off")
				if read("11").Growth.Character.Stats["grow_type"] != 0 {
					t.Fatal("legacy ignored shadow")
				}
				legacy := prepare("11", 2, 17)
				_, e := adminops.RunProfessionOperation(ctx, u, legacy, true, s.professionDefinition, offline)
				check(e)
				if read("11").Growth.Character.Stats["grow_type"] != 17 {
					t.Fatal("legacy write lost")
				}
			})
			if read("11").Growth.Character.Stats["grow_type"] != 17 {
				t.Fatal("column not synchronized")
			}
			// Public HTTP boundary: signing, CSRF, response recovery and scoped pagination.
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeAccountEditFixture(t, w, r, false)
			}))
			defer runtime.Close()
			s.deps.Runtime, e = adminapi.NewClient(runtime.URL+adminapi.Path, strings.Repeat("x", 32))
			check(e)
			token, e := s.startSession()
			check(e)
			originalRepos := s.deps.Repos
			counted := &countedProfessionReader{CharacterRepository: g.Character, CharacterSummaryReader: g.Character.(repository.CharacterSummaryReader), CharacterResourceReader: g.Character.(repository.CharacterResourceReader), ProfessionManagementReader: g.Character.(repository.ProfessionManagementReader)}
			s.deps.Repos = func() (repository.Group, bool) {
				group, ok := originalRepos()
				group.Character = counted
				return group, ok
			}
			status, body := v1TestCall(t, s, token, "GET", "/api/v1/characters/10/profession")
			if status != 200 || counted.calls != 1 {
				t.Fatal(status, body)
			}
			counted.changeJob = true
			if status, body := v1TestCall(t, s, token, "GET", "/api/v1/characters/10/profession"); status != 409 {
				t.Fatal("concurrent profession change", status, body)
			}
			s.deps.Repos = originalRepos
			payload := map[string]any{"accountId": "wallet:1", "targetJob": 2, "targetGrowType": 17, "revision": body["data"].(map[string]any)["revision"], "pvfChecksum": source.checksum, "reason": "HTTP职业调整"}
			call := func(path, key string, headers map[string]string) (int, map[string]any) {
				raw, _ := json.Marshal(payload)
				return operationCall(s, token, path, string(raw), key, headers)
			}
			path := "characters/10/profession-operations"
			status, _ = call(path+"/preview", "", map[string]string{"X-CSRF-Token": ""})
			if status != 403 {
				t.Fatal(status)
			}
			status, body = call(path+"/preview", "", nil)
			if status != 200 {
				t.Fatal(status, body)
			}
			payload["planToken"] = body["data"].(map[string]any)["planToken"]
			payload["definitionRevision"] = body["data"].(map[string]any)["definitionRevision"]
			payload["targetGrowType"] = 1
			status, _ = call(path, "profession-http-request-001", nil)
			if status != 409 {
				t.Fatal("tamper", status)
			}
			payload["targetGrowType"] = 17
			oldHash := source.checksum
			source.checksum = strings.Repeat("a", 64)
			status, _ = call(path, "profession-http-request-001", nil)
			if status == 200 {
				t.Fatal("new PVF accepted")
			}
			source.checksum = oldHash
			status, body = call(path, "profession-http-request-001", nil)
			if status != 200 {
				t.Fatal(status, body)
			}
			receipt := body["data"]
			s.deps.PVF = nil
			s.deps.Runtime = nil
			status, body = call(path, "profession-http-request-001", nil)
			if status != 200 || !reflect.DeepEqual(receipt, body["data"]) {
				t.Fatal("offline recovery", status, body)
			}
			status, body = v1TestCall(t, s, token, "GET", "/api/v1/"+path+"?limit=1")
			if status != 200 {
				t.Fatal(status, body)
			}
			cursor := url.QueryEscape(body["page"].(map[string]any)["nextCursor"].(string))
			status, _ = v1TestCall(t, s, token, "GET", "/api/v1/characters/11/profession-operations?limit=1&cursor="+cursor)
			if status != 400 {
				t.Fatal("cursor isolation", status)
			}
		})
	}
}
