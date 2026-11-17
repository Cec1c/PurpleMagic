package gmtool

import (
	"context"
	"net/http"
	"strconv"
)

func (s *Server) v1CharacterOnline(ctx context.Context, id string) any {
	if s.deps.Runtime == nil {
		return "unknown"
	}
	num, err := strconv.ParseUint(id, 10, 16)
	if err != nil || num == 0 || strconv.FormatUint(num, 10) != id {
		return "unknown"
	}
	snapshot, err := s.deps.Runtime.OnlineSnapshot(ctx)
	if err != nil {
		return "unknown"
	}
	target := uint16(num)
	for _, member := range snapshot.Members {
		if member.CharacterID == target {
			return true
		}
	}
	return false
}

func (s *Server) v1CharacterOnlineAsync(ctx context.Context, id string) <-chan any {
	ch := make(chan any, 1)
	if s.deps.Runtime == nil {
		ch <- "unknown"
		return ch
	}
	go func() {
		ch <- s.v1CharacterOnline(ctx, id)
	}()
	return ch
}

// The verified complete snapshot distinguishes offline roles from unavailable runtime state.
func (s *Server) v1Presence(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		v1Fail(w, r, 400, "invalid_query", "此接口不接受查询参数", false)
		return
	}
	if s.deps.Runtime == nil {
		v1Fail(w, r, 503, "presence_unavailable", "在线状态暂时无法读取", true)
		return
	}
	snapshot, err := s.deps.Runtime.OnlineSnapshot(r.Context())
	if err != nil {
		v1Fail(w, r, 503, "presence_unavailable", "在线状态暂时无法读取", true)
		return
	}
	ids := make([]string, 0, len(snapshot.Members))
	for _, member := range snapshot.Members {
		ids = append(ids, strconv.FormatUint(uint64(member.CharacterID), 10))
	}
	v1Reply(w, r, 200, v1Envelope{Data: map[string]any{"characterIds": ids, "observedAt": snapshot.ObservedAt, "complete": true}})
}
