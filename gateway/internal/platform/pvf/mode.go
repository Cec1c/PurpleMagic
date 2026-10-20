package pvf

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"

	"dnf90.local/gateway/internal/platform/filex"
)

// ModeSource keeps the approved disk source available while the same archive
// is loaded in the background. A reader retains its source until it returns.
type ModeSource struct {
	mu                                sync.RWMutex
	disk                              *DiskArchive
	memory                            *Archive
	statePath, hash, desired, failure string
	loading, closed                   bool
	ctx                               context.Context
	work                              sync.WaitGroup
	load                              func() (*Archive, error)
}

type ModeStatus struct {
	Mode             string `json:"mode"`
	Desired          string `json:"desired"`
	Loading          bool   `json:"loading"`
	Checksum         string `json:"pvfChecksum"`
	ArchiveBytes     int64  `json:"archiveBytes"`
	CacheBudgetBytes int    `json:"cacheBudgetBytes"`
	Error            string `json:"error,omitempty"`
}

const memoryChunkBudget = 64 << 20
const memoryTextBudget = 32 << 20

type archiveCache struct {
	mu     sync.Mutex
	chunks *boundedCache[int, []byte]
	texts  *boundedCache[int, string]
}

func OpenModeSource(ctx context.Context, path, index, hash, statePath string) (*ModeSource, error) {
	disk, err := OpenDisk(path, index, hash)
	if err != nil {
		return nil, err
	}
	m := &ModeSource{disk: disk, statePath: statePath, hash: disk.Snapshot().Checksum, desired: "disk", ctx: ctx}
	m.load = func() (*Archive, error) {
		bundle, e := Load(Options{Path: path})
		if e != nil {
			return nil, e
		}
		if bundle.Snapshot().Checksum != m.hash {
			return nil, errors.New("PVF checksum changed")
		}
		a, e := OpenArchive(bundle)
		if e != nil {
			return nil, e
		}
		a.limited = &archiveCache{chunks: newBoundedCache[int, []byte](memoryChunkBudget), texts: newBoundedCache[int, string](memoryTextBudget)}
		// Preheat common directories only; never expand the entire decoded PVF.
		for _, p := range []string{"stackable/stackable.lst", "equipment/equipment.lst", "n_quest/quest.lst"} {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if _, ok := a.ResolvePath(p); ok {
				if _, e := a.ReadText(p); e != nil {
					return nil, e
				}
			}
		}
		return a, nil
	}
	if statePath != "" {
		raw, e := os.ReadFile(statePath)
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			m.failure = "PVF 模式记录读取失败，继续使用磁盘索引；请重新保存设置"
			return m, nil
		}
		if e == nil {
			var saved struct {
				Mode string `json:"mode"`
				Hash string `json:"pvfChecksum"`
			}
			if json.Unmarshal(raw, &saved) != nil || (saved.Mode != "disk" && saved.Mode != "memory") {
				m.failure = "PVF 模式记录无效，继续使用磁盘索引；请重新保存设置"
				return m, nil
			}
			if saved.Hash == m.hash && saved.Mode == "memory" {
				m.desired = "memory"
				m.startLocked()
			}
		}
	}
	return m, nil
}

func (m *ModeSource) startLocked() {
	if m.loading || m.memory != nil {
		return
	}
	m.loading = true
	m.work.Add(1)
	go func() {
		defer m.work.Done()
		a, err := m.load()
		m.mu.Lock()
		defer m.mu.Unlock()
		m.loading = false
		if m.closed || m.ctx.Err() != nil || m.desired != "memory" {
			return
		}
		if err != nil || a == nil || a.Snapshot().Checksum != m.hash {
			m.failure = "PVF 内存加载失败，继续使用磁盘索引；可重试加载"
			return
		}
		m.memory = a
		m.failure = ""
	}()
}

func (m *ModeSource) SetMode(mode string) error {
	if mode != "disk" && mode != "memory" {
		return errors.New("invalid PVF mode")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.ctx.Err() != nil {
		return errors.New("PVF source closed")
	}
	if m.statePath != "" {
		raw, _ := json.Marshal(map[string]string{"mode": mode, "pvfChecksum": m.hash})
		if err := filex.AtomicWriteFile(m.statePath, raw, 0600); err != nil {
			return errors.New("PVF 模式保存失败")
		}
	}
	m.desired, m.failure = mode, ""
	if mode == "disk" {
		m.memory = nil
	} else {
		m.startLocked()
	}
	return nil
}

func (m *ModeSource) ModeStatus() ModeStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := ModeStatus{Mode: "disk", Desired: m.desired, Loading: m.loading, Checksum: m.hash, ArchiveBytes: m.disk.Snapshot().Size, CacheBudgetBytes: diskChunkBudget + diskTextBudget + diskDirectoryBudget, Error: m.failure}
	if m.memory != nil {
		s.Mode = "memory"
		s.CacheBudgetBytes += memoryChunkBudget + memoryTextBudget
	}
	return s
}
func (m *ModeSource) Snapshot() ArchiveSnapshot {
	// Identity is immutable across modes; do not expose mutable source pointers.
	return m.disk.Snapshot()
}
func (m *ModeSource) ReadText(path string) (string, error) {
	m.mu.RLock()
	a, closed := m.memory, m.closed
	m.mu.RUnlock()
	if closed {
		return "", ErrDiskClosed
	}
	if a != nil {
		return a.ReadText(path)
	}
	return m.disk.ReadText(path)
}
func (m *ModeSource) ResolvePath(path string) (string, bool) {
	m.mu.RLock()
	a, closed := m.memory, m.closed
	m.mu.RUnlock()
	if closed {
		return "", false
	}
	if a != nil {
		return a.ResolvePath(path)
	}
	return m.disk.ResolvePath(path)
}
func (m *ModeSource) Close() error {
	m.mu.Lock()
	m.closed = true
	m.memory = nil
	m.mu.Unlock()
	m.work.Wait()
	return m.disk.Close()
}
