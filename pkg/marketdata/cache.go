package marketdata

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type snapshot struct {
	Version   int             `json:"version"`
	Key       string          `json:"key"`
	Data      json.RawMessage `json:"data"`
	FetchedAt time.Time       `json:"fetchedAt"`
	ExpiresAt time.Time       `json:"expiresAt"`
}

type snapshotCache struct {
	dir     string
	mu      sync.RWMutex
	entries map[string]snapshot
}

func newSnapshotCache(dir string) (*snapshotCache, error) {
	if dir == "" {
		return nil, errors.New("cache directory is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &snapshotCache{dir: dir, entries: make(map[string]snapshot)}, nil
}

func (c *snapshotCache) get(key string) (snapshot, bool) {
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if ok {
		return entry, true
	}

	content, err := os.ReadFile(c.path(key))
	if err != nil {
		return snapshot{}, false
	}
	if err := json.Unmarshal(content, &entry); err != nil || entry.Version != CacheVersion || entry.Key != key {
		_ = os.Rename(c.path(key), c.path(key)+".corrupt")
		return snapshot{}, false
	}
	c.mu.Lock()
	c.entries[key] = entry
	c.mu.Unlock()
	return entry, true
}

func (c *snapshotCache) set(entry snapshot) error {
	c.mu.Lock()
	c.entries[entry.Key] = entry
	c.mu.Unlock()

	content, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	target := c.path(entry.Key)
	tmp, err := os.CreateTemp(c.dir, "marketdata-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		// Windows cannot always replace an existing destination atomically.
		if removeErr := os.Remove(target); removeErr != nil && !os.IsNotExist(removeErr) {
			return err
		}
		if err := os.Rename(tmpName, target); err != nil {
			return err
		}
	}
	return nil
}

func (c *snapshotCache) path(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(c.dir, hex.EncodeToString(sum[:])+".json")
}
