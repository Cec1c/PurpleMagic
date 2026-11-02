package gmtool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/moderation"
	"dnf90.local/gateway/internal/servicehttp"
)

const maxRangeAddresses = 4096

type ipRangePreview struct {
	Target string `json:"target"`
	First  string `json:"first"`
	Last   string `json:"last"`
	Count  int    `json:"count"`
}

// Expand only a bounded IPv4 network. Every address, including .0 and .255,
// becomes an ordinary game-owned single-IP ban; no new game contract is used.
func previewIPRange(raw string) (ipRangePreview, error) {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "*") {
		parts := strings.Split(raw, ".")
		if len(parts) != 4 {
			return ipRangePreview{}, errors.New("请输入 IPv4 网段，例如 192.0.2.* 或 192.0.2.0/24")
		}
		bits, wildcard := 0, false
		for i, p := range parts {
			if p == "*" {
				wildcard = true
				parts[i] = "0"
			} else if wildcard {
				return ipRangePreview{}, errors.New("通配符只能连续出现在地址末尾")
			} else {
				bits += 8
			}
		}
		raw = strings.Join(parts, ".") + "/" + strconv.Itoa(bits)
	}
	p, err := netip.ParsePrefix(raw)
	if err != nil || !p.Addr().Is4() {
		return ipRangePreview{}, errors.New("请输入 IPv4 网段，例如 192.0.2.* 或 192.0.2.0/24")
	}
	if p.Bits() < 20 {
		return ipRangePreview{}, errors.New("一次最多封禁 4096 个地址，请使用 /20 或更小的网段")
	}
	p = p.Masked()
	n := 1 << (32 - p.Bits())
	last := p.Addr()
	for i := 1; i < n; i++ {
		last = last.Next()
	}
	return ipRangePreview{p.String(), p.Addr().String(), last.String(), n}, nil
}

type ipRangeInput struct {
	Target    string     `json:"target"`
	Reason    string     `json:"reason"`
	ExpiresAt *time.Time `json:"expiresAt"`
	RequestID string     `json:"requestId"`
}
type ipRangeMember struct {
	Command  moderation.BanChange  `json:"command"`
	Previous *moderation.Ban       `json:"previous,omitempty"`
	State    string                `json:"state"`
	Sent     bool                  `json:"sent,omitempty"`
	Owned    uint64                `json:"owned,omitempty"`
	Revert   *moderation.BanChange `json:"revert,omitempty"`
}
type ipRangeJob struct {
	ipRangeInput
	ID        string          `json:"id"`
	CreatedAt time.Time       `json:"createdAt"`
	Release   bool            `json:"release"`
	Members   []ipRangeMember `json:"members"`
	Error     string          `json:"error,omitempty"`
}
type ipRangeView struct {
	ipRangePreview
	ID        string     `json:"id"`
	Reason    string     `json:"reason"`
	ExpiresAt *time.Time `json:"expiresAt"`
	CreatedAt time.Time  `json:"createdAt"`
	Status    string     `json:"status"`
	Applied   int        `json:"applied"`
	Retained  int        `json:"retained"`
	Conflict  int        `json:"conflict"`
	Pending   int        `json:"pending"`
	Released  int        `json:"released"`
	Error     string     `json:"error,omitempty"`
}

func (j ipRangeJob) view() ipRangeView {
	p, _ := previewIPRange(j.Target)
	v := ipRangeView{ipRangePreview: p, ID: j.ID, Reason: j.Reason, ExpiresAt: j.ExpiresAt, CreatedAt: j.CreatedAt, Status: "active", Error: j.Error}
	for _, m := range j.Members {
		switch m.State {
		case "pending":
			v.Pending++
		case "applied":
			v.Applied++
			if j.Release {
				v.Pending++
			}
		case "retained":
			v.Retained++
		case "conflict":
			v.Conflict++
		case "released":
			v.Released++
		}
	}
	if v.Conflict > 0 {
		v.Status = "partial"
	}
	if v.Pending > 0 {
		v.Status = "applying"
	}
	if j.ExpiresAt != nil && !j.ExpiresAt.After(time.Now()) {
		v.Status = "expired"
	}
	if j.Release {
		v.Status = "released"
		if v.Pending > 0 {
			v.Status = "releasing"
		}
	}
	return v
}

type ipRangeQueue struct {
	mu     sync.Mutex
	worker sync.Mutex
	path   string
	peer   *servicehttp.Client
	jobs   map[string]ipRangeJob
}

func openIPRangeQueue(path string, peer *servicehttp.Client) (*ipRangeQueue, error) {
	q := &ipRangeQueue{path: path, peer: peer, jobs: map[string]ipRangeJob{}}
	raw, e := os.ReadFile(path)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	if e == nil {
		if len(raw) > 16<<20 || json.Unmarshal(raw, &q.jobs) != nil || q.jobs == nil || len(q.jobs) > 128 {
			return nil, errors.New("invalid IP range queue")
		}
		for id, j := range q.jobs {
			p, e := previewIPRange(j.Target)
			if e != nil || !queueID(id) || j.ID != id || p.Target != j.Target || len(j.Members) != p.Count {
				return nil, errors.New("invalid IP range job")
			}
			a := netip.MustParseAddr(p.First)
			for _, m := range j.Members {
				if m.Command.Kind != "ip" || m.Command.Target != a.String() || m.Command.Revoke || (m.State != "pending" && m.State != "applied" && m.State != "retained" && m.State != "conflict" && m.State != "released") {
					return nil, errors.New("invalid IP range member")
				}
				a = a.Next()
			}
		}
	}
	return q, nil
}
func (q *ipRangeQueue) save() error {
	raw, e := json.Marshal(q.jobs)
	if e != nil {
		return e
	}
	if len(raw) > 16<<20 {
		return errors.New("IP range queue full")
	}
	f, e := os.CreateTemp(filepath.Dir(q.path), ".ip-ranges-")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(raw)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	if e = os.Rename(name, q.path); e != nil {
		return e
	}
	if d, e := os.Open(filepath.Dir(q.path)); e == nil {
		d.Sync()
		d.Close()
	}
	return nil
}
func rangeID(raw string) string { h := sha256.Sum256([]byte(raw)); return hex.EncodeToString(h[:16]) }
func (q *ipRangeQueue) bans(ctx context.Context, params url.Values) (moderation.BanPage, error) {
	var out struct {
		Data moderation.BanPage `json:"data"`
	}
	res, e := q.peer.Do(ctx, "GET", "/api/v1/anti-cheat/bans?"+params.Encode(), nil, nil)
	if e != nil {
		return out.Data, e
	}
	raw, e := q.peer.ReadResponse(ctx, res)
	if e == nil {
		e = json.Unmarshal(raw, &out)
	}
	return out.Data, e
}
func (q *ipRangeQueue) create(ctx context.Context, in ipRangeInput) (ipRangeView, error) {
	p, e := previewIPRange(in.Target)
	if e != nil {
		return ipRangeView{}, e
	}
	in.Target = p.Target
	in.Reason = strings.TrimSpace(in.Reason)
	check := moderation.BanChange{Kind: "ip", Target: p.First, Reason: in.Reason, RequestID: in.RequestID}
	if e = check.Normalize(time.Now()); e != nil {
		return ipRangeView{}, errors.New("请填写有效的原因和请求编号")
	}
	id := rangeID("ip-range:" + in.RequestID)
	q.mu.Lock()
	old, exists := q.jobs[id]
	if exists {
		defer q.mu.Unlock()
		if !sameRangeInput(old.ipRangeInput, in) {
			return ipRangeView{}, errors.New("请求编号已用于其他操作")
		}
		return old.view(), nil
	}
	q.mu.Unlock()
	check.ExpiresAt = in.ExpiresAt
	if e = check.Normalize(time.Now()); e != nil {
		return ipRangeView{}, errors.New("封禁到期时间无效")
	}
	// One read pass gets current revisions; each ordinary write still validates
	// its revision authoritatively, so concurrent administrator edits win.
	prior := map[string]moderation.Ban{}
	for offset := 0; ; offset += 100 {
		if offset > 100000 {
			return ipRangeView{}, errors.New("封禁记录过多，请缩小记录范围后重试")
		}
		page, e := q.bans(ctx, url.Values{"limit": {"100"}, "offset": {strconv.Itoa(offset)}, "sortBy": {"target"}, "sortOrder": {"asc"}})
		if e != nil {
			return ipRangeView{}, e
		}
		for _, b := range page.Rows {
			if b.Kind == "ip" {
				prior[b.Target] = b
			}
		}
		if !page.HasMore {
			break
		}
	}
	j := ipRangeJob{ipRangeInput: in, ID: id, CreatedAt: time.Now().UTC()}
	a := netip.MustParseAddr(p.First)
	for i := 0; i < p.Count; i++ {
		ip := a.String()
		a = a.Next()
		m := ipRangeMember{State: "pending", Command: moderation.BanChange{Kind: "ip", Target: ip, Reason: in.Reason, ExpiresAt: in.ExpiresAt, RequestID: rangeID(id + ":" + ip + ":apply")}}
		if b, ok := prior[ip]; ok {
			m.Command.Revision = b.Revision
			m.Previous = &b
			if b.Active && (b.ExpiresAt == nil || in.ExpiresAt != nil && !b.ExpiresAt.Before(*in.ExpiresAt)) {
				m.State = "retained"
			}
		}
		j.Members = append(j.Members, m)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if old, exists = q.jobs[id]; exists {
		if !sameRangeInput(old.ipRangeInput, in) {
			return ipRangeView{}, errors.New("请求编号已用于其他操作")
		}
		return old.view(), nil
	}
	if len(q.jobs) >= 128 {
		return ipRangeView{}, errors.New("网段记录已满")
	}
	budget := p.Count
	network := netip.MustParsePrefix(p.Target)
	for _, old := range q.jobs {
		v := old.view()
		if v.Status == "released" || v.Status == "expired" {
			continue
		}
		budget += len(old.Members)
		other := netip.MustParsePrefix(old.Target)
		if network.Overlaps(other) {
			return ipRangeView{}, errors.New("已有重叠网段规则，请先解除原规则")
		}
	}
	if budget > maxRangeAddresses {
		return ipRangeView{}, errors.New("同时生效的网段最多合计 4096 个地址")
	}
	q.jobs[id] = j
	if e = q.save(); e != nil {
		delete(q.jobs, id)
		return ipRangeView{}, e
	}
	return j.view(), nil
}
func sameRangeInput(a, b ipRangeInput) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func (q *ipRangeQueue) release(id string) (ipRangeView, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.jobs[id]
	if !ok {
		return ipRangeView{}, errors.New("网段规则不存在")
	}
	old := j
	j.Release = true
	q.jobs[id] = j
	if e := q.save(); e != nil {
		q.jobs[id] = old
		return ipRangeView{}, e
	}
	return j.view(), nil
}

func (q *ipRangeQueue) step(ctx context.Context) (bool, error) {
	q.worker.Lock()
	defer q.worker.Unlock()
	q.mu.Lock()
	ids := make([]string, 0, len(q.jobs))
	for id := range q.jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var job ipRangeJob
	index := -1
	for _, id := range ids {
		j := q.jobs[id]
		for i, m := range j.Members {
			if m.State == "pending" || j.Release && m.State == "applied" {
				job = j
				index = i
				break
			}
		}
		if index >= 0 {
			break
		}
	}
	if index < 0 {
		q.mu.Unlock()
		return false, nil
	}
	m := job.Members[index]
	if m.State == "pending" && !m.Sent && (job.Release || job.ExpiresAt != nil && !job.ExpiresAt.After(time.Now())) {
		m.State = "released"
		e := q.storeMemberLocked(job.ID, index, m, "")
		q.mu.Unlock()
		return true, e
	}
	command := m.Command
	if m.State == "applied" {
		if m.Revert == nil {
			c := moderation.BanChange{Kind: "ip", Target: m.Command.Target, Revision: m.Owned, Revoke: true, Reason: "解除网段 " + job.Target, RequestID: rangeID(job.ID + ":" + m.Command.Target + ":release")}
			if b := m.Previous; b != nil && b.Active && (b.ExpiresAt == nil || b.ExpiresAt.After(time.Now())) {
				c.Revoke = false
				c.Reason = b.Reason
				c.ExpiresAt = b.ExpiresAt
			}
			m.Revert = &c
		}
		command = *m.Revert
	}
	m.Sent = true
	if e := q.storeMemberLocked(job.ID, index, m, ""); e != nil {
		q.mu.Unlock()
		return true, e
	}
	q.mu.Unlock()
	var out struct {
		Data moderation.Ban `json:"data"`
	}
	err := q.peer.JSON(ctx, "/api/v1/anti-cheat/bans", command, &out)
	q.mu.Lock()
	defer q.mu.Unlock()
	message := ""
	if err != nil {
		var pe *servicehttp.PeerError
		if errors.As(err, &pe) && (pe.Status == 400 || pe.Status == 409) {
			if command.ExpiresAt != nil && !command.ExpiresAt.After(time.Now()) && pe.Status == 400 {
				if m.Revert != nil {
					c := *m.Revert
					c.Revoke = true
					c.ExpiresAt = nil
					c.RequestID = rangeID(job.ID + ":" + m.Command.Target + ":release-expired")
					m.Revert = &c
				} else {
					m.State = "released"
				}
			} else {
				m.State = "conflict"
				message = "部分 IP 已由其他操作修改，保留其当前规则"
			}
		} else {
			message = "连接暂未取得回执，后台自动重试"
		}
	} else if out.Data.Kind != command.Kind || out.Data.Target != command.Target || out.Data.Revision != command.Revision+1 || out.Data.Revoked != command.Revoke {
		err = errors.New("invalid IP ban receipt")
		message = "回执校验未通过，后台自动重试"
	} else if m.Revert != nil {
		m.State = "released"
	} else {
		m.State = "applied"
		m.Owned = out.Data.Revision
	}
	if e := q.storeMemberLocked(job.ID, index, m, message); e != nil {
		return true, e
	}
	return true, err
}
func (q *ipRangeQueue) storeMemberLocked(id string, index int, m ipRangeMember, message string) error {
	j := q.jobs[id]
	old := j.Members[index]
	oldMessage := j.Error
	j.Members[index] = m
	j.Error = message
	q.jobs[id] = j
	if e := q.save(); e != nil {
		j.Members[index] = old
		j.Error = oldMessage
		q.jobs[id] = j
		return e
	}
	return nil
}
func (q *ipRangeQueue) run(ctx context.Context) {
	for {
		run, cancel := context.WithTimeout(ctx, 8*time.Second)
		worked, e := q.step(run)
		cancel()
		delay := 50 * time.Millisecond
		if !worked {
			delay = time.Second
		}
		if e != nil {
			delay = 5 * time.Second
		}
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

func (q *ipRangeQueue) handle(w http.ResponseWriter, r *http.Request) bool {
	const base = "/api/v1/anti-cheat/ip-ranges"
	if r.URL.Path == "/api/v1/anti-cheat/bans" && r.Method == "GET" && r.URL.Query().Get("hideRangeMembers") == "true" {
		q.groupedBans(w, r)
		return true
	}
	if r.URL.Path != base && !strings.HasPrefix(r.URL.Path, base+"/") {
		return false
	}
	if r.Method == "GET" && r.URL.Path == base+"/preview" {
		p, e := previewIPRange(r.URL.Query().Get("target"))
		if e != nil {
			v1Fail(w, r, 400, "invalid_ip_range", e.Error(), false)
		} else {
			v1Reply(w, r, 200, v1Envelope{Data: p})
		}
		return true
	}
	if r.Method == "GET" && r.URL.Path == base {
		q.mu.Lock()
		rows := []ipRangeView{}
		for _, j := range q.jobs {
			rows = append(rows, j.view())
		}
		q.mu.Unlock()
		sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })
		v1Reply(w, r, 200, v1Envelope{Data: rows})
		return true
	}
	if r.Method != "POST" || r.URL.RawQuery != "" {
		v1Fail(w, r, 400, "invalid_ip_range", "网段请求无效", false)
		return true
	}
	var view ipRangeView
	var e error
	if r.URL.Path == base {
		var in ipRangeInput
		if !v1DecodeLimit(w, r, &in, 4096) {
			return true
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		view, e = q.create(ctx, in)
	} else {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, base+"/"), "/")
		if len(parts) != 2 || !queueID(parts[0]) || parts[1] != "release" {
			v1Fail(w, r, 404, "not_found", "网段规则不存在", false)
			return true
		}
		var in struct {
			Confirm bool `json:"confirm"`
		}
		if !v1DecodeLimit(w, r, &in, 1024) {
			return true
		}
		if !in.Confirm {
			v1Fail(w, r, 400, "confirmation_required", "请确认解除网段封禁", false)
			return true
		}
		view, e = q.release(parts[0])
	}
	if e != nil {
		var pe *servicehttp.PeerError
		if errors.As(e, &pe) {
			v1Fail(w, r, 503, "range_peer_unavailable", "读取封禁状态失败，请重试", true)
		} else if e.Error() == "" || e.Error()[0] < 128 {
			v1Fail(w, r, 503, "range_store_unavailable", "保存网段任务失败，请重试", true)
		} else {
			v1Fail(w, r, 409, "range_conflict", e.Error(), false)
		}
	} else {
		v1Reply(w, r, 200, v1Envelope{Data: view})
	}
	return true
}

// The ordinary list omits only revisions owned by a range rule. Independent
// bans and later administrator edits remain visible, with correct pagination.
func (q *ipRangeQueue) groupedBans(w http.ResponseWriter, r *http.Request) {
	params := r.URL.Query()
	params.Del("hideRangeMembers")
	offset, offsetErr := strconv.Atoi(params.Get("offset"))
	limit := 50
	if v := params.Get("limit"); v != "" {
		limit, _ = strconv.Atoi(v)
	}
	if params.Get("offset") != "" && offsetErr != nil || offset < 0 || offset > 100000 || limit < 1 || limit > 100 {
		v1Fail(w, r, 400, "invalid_query", "查询范围无效", false)
		return
	}
	q.mu.Lock()
	owned := map[string]uint64{}
	for _, j := range q.jobs {
		for _, m := range j.Members {
			if m.Owned > 0 && m.State == "applied" {
				owned[m.Command.Target] = m.Owned
			}
		}
	}
	q.mu.Unlock()
	params.Set("limit", "100")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	out := moderation.BanPage{Rows: []moderation.Ban{}}
	seen := 0
	for rawOffset := 0; rawOffset <= 100000; rawOffset += 100 {
		params.Set("offset", fmt.Sprint(rawOffset))
		page, e := q.bans(ctx, params)
		if e != nil {
			v1Fail(w, r, 503, "range_list_unavailable", "读取封禁记录失败，请重试", true)
			return
		}
		for _, b := range page.Rows {
			if b.Kind == "ip" && owned[b.Target] == b.Revision {
				continue
			}
			if seen >= offset {
				out.Rows = append(out.Rows, b)
			}
			seen++
			if len(out.Rows) > limit {
				out.Rows = out.Rows[:limit]
				out.HasMore = true
				v1Reply(w, r, 200, v1Envelope{Data: out})
				return
			}
		}
		if !page.HasMore {
			v1Reply(w, r, 200, v1Envelope{Data: out})
			return
		}
	}
	v1Fail(w, r, 503, "range_list_unavailable", "封禁记录读取范围已满", true)
}
