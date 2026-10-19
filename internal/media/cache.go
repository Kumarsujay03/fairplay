package media

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// CacheEntry is one prepared (or half-prepared) video in the cache folder.
type CacheEntry struct {
	Key      string    `json:"key"`
	Name     string    `json:"name"`
	Bytes    int64     `json:"bytes"`
	Done     bool      `json:"done"`
	LastUsed time.Time `json:"lastUsed"`
	InUse    bool      `json:"inUse"` // the room is showing it or it is being encoded
}

// CacheUsage lists everything in the cache folder, newest first.
func CacheUsage(dir string) ([]CacheEntry, int64) {
	entries, _ := os.ReadDir(dir)
	out := []CacheEntry{} // never nil: an empty cache is [] in JSON
	var total int64
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "emoji" { // reaction animations live next to the videos
			continue
		}
		p := filepath.Join(dir, e.Name())
		ce := CacheEntry{Key: e.Name(), Done: fileExists(filepath.Join(p, ".done")), Name: "Unfinished encode"}
		if b, err := os.ReadFile(filepath.Join(p, "status.json")); err == nil {
			var st Status
			if json.Unmarshal(b, &st) == nil && st.Name != "" {
				ce.Name = st.Name
			}
		}
		if info, err := os.Stat(p); err == nil {
			ce.LastUsed = info.ModTime()
		}
		_ = filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				if fi, err := d.Info(); err == nil {
					ce.Bytes += fi.Size()
				}
			}
			return nil
		})
		total += ce.Bytes
		out = append(out, ce)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastUsed.After(out[j].LastUsed) })
	return out, total
}

// inUseKeys are cache folders that must not be deleted right now.
func (m *Manager) inUseKeys(current string) map[string]bool {
	keep := map[string]bool{}
	m.mu.Lock()
	defer m.mu.Unlock()
	if st, ok := m.statuses[current]; ok && st.key != "" {
		keep[st.key] = true
	} else if it, ok := m.items[current]; ok {
		// Not prepared since the start: its folder may still hold an
		// interrupted encode to continue.
		keep[m.key(it)] = true
	}
	if m.running != nil {
		if st, ok := m.statuses[m.running.id]; ok {
			keep[st.key] = true
		}
	}
	return keep
}

// Usage reports the cache, marking what is in use. current is the room's video.
func (m *Manager) Usage(current string) ([]CacheEntry, int64, string) {
	list, total := CacheUsage(m.cache)
	keep := m.inUseKeys(current)
	for i := range list {
		list[i].InUse = keep[list[i].Key]
	}
	return list, total, m.cache
}

// ClearCache deletes every prepared video except the one the room is showing
// and one still encoding. It returns the bytes freed.
func (m *Manager) ClearCache(current string) int64 {
	keep := m.inUseKeys(current)
	list, _ := CacheUsage(m.cache)
	var freed int64
	for _, e := range list {
		if keep[e.Key] {
			continue
		}
		if os.RemoveAll(filepath.Join(m.cache, e.Key)) == nil {
			freed += e.Bytes
			m.forgetKey(e.Key)
		}
	}
	return freed
}

// Prune removes the least recently used prepared videos until the cache is
// under maxBytes. In-use folders are never removed. maxBytes <= 0 disables it.
func (m *Manager) Prune(maxBytes int64, current string) int64 {
	if maxBytes <= 0 {
		return 0
	}
	keep := m.inUseKeys(current)
	list, total := CacheUsage(m.cache)
	var freed int64
	for i := len(list) - 1; i >= 0 && total > maxBytes; i-- { // oldest first
		e := list[i]
		if keep[e.Key] {
			continue
		}
		if os.RemoveAll(filepath.Join(m.cache, e.Key)) == nil {
			total -= e.Bytes
			freed += e.Bytes
			m.forgetKey(e.Key)
		}
	}
	return freed
}

// forgetKey drops in-memory statuses that pointed at a deleted folder, so the
// file is prepared again next time it is played.
func (m *Manager) forgetKey(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, st := range m.statuses {
		if st.key == key && (m.running == nil || m.running.id != id) {
			delete(m.statuses, id)
		}
	}
}

// touch marks a cache folder as recently used so pruning keeps it longer.
func touch(dir string) {
	now := time.Now()
	_ = os.Chtimes(dir, now, now)
}
