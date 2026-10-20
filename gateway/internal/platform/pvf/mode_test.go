package pvf

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestModeSourceApprovedPVF(t *testing.T) {
	path, index, hash := os.Getenv("PURPLEMAGIC_PVF_MODE_TEST_PATH"), os.Getenv("PURPLEMAGIC_PVF_MODE_TEST_INDEX"), os.Getenv("PURPLEMAGIC_PVF_MODE_TEST_SHA256")
	if path == "" || index == "" || len(hash) != 64 {
		t.Skip("approved PVF mode measurement is opt-in")
	}
	heap := func() uint64 { runtime.GC(); var m runtime.MemStats; runtime.ReadMemStats(&m); return m.HeapAlloc }
	base := heap()
	start := time.Now()
	m, err := OpenModeSource(context.Background(), path, index, hash, "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	diskHeap := heap()
	diskOpen := time.Since(start)
	start = time.Now()
	if err = m.SetMode("memory"); err != nil {
		t.Fatal(err)
	}
	m.work.Wait()
	load := time.Since(start)
	if m.ModeStatus().Mode != "memory" {
		t.Fatal(m.ModeStatus())
	}
	memoryHeap := heap()
	paths := []string{}
	counts := map[string]int{}
	for _, f := range m.memory.files {
		for _, prefix := range []string{"equipment/", "stackable/", "n_quest/"} {
			if strings.HasPrefix(f.ArchivePath, prefix) && counts[prefix] < 20 {
				paths = append(paths, f.ArchivePath)
				counts[prefix]++
				break
			}
		}
	}
	measure := func() (time.Duration, [][32]byte) {
		t.Helper()
		start := time.Now()
		sums := [][32]byte{}
		for _, p := range paths {
			text, e := m.ReadText(p)
			if e != nil {
				t.Fatal(e)
			}
			sums = append(sums, sha256.Sum256([]byte(text)))
		}
		return time.Since(start), sums
	}
	cold, expected := measure()
	warm, _ := measure()
	memoryReadHeap := heap()
	m.SetMode("disk")
	releasedHeap := heap()
	diskRead, actual := measure()
	for i := range expected {
		if expected[i] != actual[i] {
			t.Fatal("mode changes definition", i)
		}
	}
	if memoryHeap <= diskHeap || releasedHeap >= memoryReadHeap {
		t.Fatal("memory archive not released", diskHeap, memoryHeap, releasedHeap)
	}
	report := map[string]any{"pvfSHA256": hash, "samples": len(paths), "baseHeap": base, "diskHeap": diskHeap, "memoryHeap": memoryHeap, "memoryReadHeap": memoryReadHeap, "releasedHeap": releasedHeap, "diskOpenMs": diskOpen.Milliseconds(), "memoryLoadMs": load.Milliseconds(), "memoryColdReadMs": cold.Milliseconds(), "memoryWarmReadMs": warm.Milliseconds(), "diskReadMs": diskRead.Milliseconds()}
	raw, _ := json.Marshal(report)
	t.Log(string(raw))
}

func TestModeSourceSwitchPersistAndReadConcurrently(t *testing.T) {
	path, index, hash, ref := diskFixture(t, true)
	state := filepath.Join(t.TempDir(), "mode.json")
	m, err := OpenModeSource(context.Background(), path, index, hash, state)
	if err != nil {
		t.Fatal(err)
	}
	file := ref.files[0].ArchivePath
	want, err := m.ReadText(file)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 100 {
				got, e := m.ReadText(file)
				if e != nil || got != want {
					t.Errorf("concurrent source read: %v", e)
				}
			}
		}()
	}
	if err = m.SetMode("memory"); err != nil {
		t.Fatal(err)
	}
	m.work.Wait()
	if s := m.ModeStatus(); s.Mode != "memory" || s.Checksum != hash || s.Loading {
		t.Fatal(s)
	}
	if m.memory.limited == nil {
		t.Fatal("memory decoder caches must be bounded")
	}
	workers.Wait()
	m.Close()
	m, err = OpenModeSource(context.Background(), path, index, hash, state)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	m.work.Wait()
	if m.ModeStatus().Mode != "memory" {
		t.Fatal("persisted mode not restored")
	}
	if err = m.SetMode("disk"); err != nil {
		t.Fatal(err)
	}
	if m.memory != nil || m.ModeStatus().Mode != "disk" {
		t.Fatal("memory archive retained")
	}
	got, err := m.ReadText(file)
	if err != nil || got != want {
		t.Fatal("fallback read differs", err)
	}
}

func TestModeSourceLoadingFailureCancellationAndHashPinning(t *testing.T) {
	path, index, hash, _ := diskFixture(t, false)
	m, err := OpenModeSource(context.Background(), path, index, hash, "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	started, release := make(chan struct{}), make(chan struct{})
	load := m.load
	m.load = func() (*Archive, error) { close(started); <-release; return load() }
	if err = m.SetMode("memory"); err != nil {
		t.Fatal(err)
	}
	<-started
	if s := m.ModeStatus(); !s.Loading || s.Mode != "disk" {
		t.Fatal(s)
	}
	if err = m.SetMode("disk"); err != nil {
		t.Fatal(err)
	}
	close(release)
	m.work.Wait()
	if m.ModeStatus().Mode != "disk" {
		t.Fatal("cancelled load published")
	}
	m.load = func() (*Archive, error) { return nil, errors.New("private path must not leak") }
	m.SetMode("memory")
	m.work.Wait()
	if s := m.ModeStatus(); s.Mode != "disk" || s.Error == "" || strings.Contains(s.Error, "private") {
		t.Fatal(s)
	}
	m.load = load
	raw, _ := os.ReadFile(path)
	raw[len(raw)-1] ^= 1
	os.WriteFile(path, raw, 0600)
	m.SetMode("memory")
	m.work.Wait()
	if s := m.ModeStatus(); s.Mode != "disk" || s.Error == "" || s.Checksum != hash {
		t.Fatal(s)
	}
	if m.SetMode("arbitrary") == nil {
		t.Fatal("invalid mode accepted")
	}
}

func TestModeSourceInvalidStateFallsBackAndSaveFailurePreservesMode(t *testing.T) {
	path, index, hash, _ := diskFixture(t, false)
	state := filepath.Join(t.TempDir(), "mode.json")
	if err := os.WriteFile(state, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := OpenModeSource(context.Background(), path, index, hash, state)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if s := m.ModeStatus(); s.Mode != "disk" || s.Error == "" {
		t.Fatal(s)
	}
	if err = m.SetMode("memory"); err != nil {
		t.Fatal(err)
	}
	m.work.Wait()
	if m.ModeStatus().Mode != "memory" {
		t.Fatal(m.ModeStatus())
	}
	// A regular file cannot be the parent of a mode state file.
	m.statePath = filepath.Join(state, "unwritable.json")
	if m.SetMode("disk") == nil || m.ModeStatus().Mode != "memory" {
		t.Fatal("failed save changed active mode")
	}
}
