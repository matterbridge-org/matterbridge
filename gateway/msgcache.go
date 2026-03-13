package gateway

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// PersistentMsgEntry represents a single downstream message ID mapping.
type PersistentMsgEntry struct {
	Protocol   string    `json:"protocol"`
	BridgeName string    `json:"bridge_name"`
	ID         string    `json:"id"`
	ChannelID  string    `json:"channel_id"`
	CreatedAt  time.Time `json:"created_at,omitempty"`
}

// PersistentMsgCache is a file-backed message ID cache that persists
// cross-bridge message ID mappings across restarts.
type PersistentMsgCache struct {
	mu        sync.Mutex
	path      string
	data      map[string][]PersistentMsgEntry
	dirty     bool
	ticker    *time.Ticker
	stopCh    chan struct{}
	logger    *logrus.Entry
	maxAge    time.Duration
	lastPrune time.Time
}

const defaultMaxAge = 168 * time.Hour // 7 days
const pruneInterval = 1 * time.Hour

// NewPersistentMsgCache creates a new persistent cache backed by the given file path.
// Returns nil if path is empty. Loads existing data on creation and starts a
// background flush loop that writes changes to disk every 30 seconds.
// maxAge controls how long message ID entries are kept; zero uses the default (7 days).
func NewPersistentMsgCache(path string, maxAge time.Duration, logger *logrus.Entry) *PersistentMsgCache {
	if path == "" {
		return nil
	}
	if maxAge <= 0 {
		maxAge = defaultMaxAge
	}
	c := &PersistentMsgCache{
		path:   path,
		data:   make(map[string][]PersistentMsgEntry),
		stopCh: make(chan struct{}),
		logger: logger,
		maxAge: maxAge,
	}
	c.load()
	c.prune() // clean up stale entries on startup
	c.ticker = time.NewTicker(30 * time.Second)
	go c.flushLoop()
	return c
}

func (c *PersistentMsgCache) load() {
	f, err := os.ReadFile(c.path)
	if err != nil {
		if !os.IsNotExist(err) {
			c.logger.Warnf("failed to read message cache %s: %s", c.path, err)
		}
		return
	}
	if err := json.Unmarshal(f, &c.data); err != nil {
		c.logger.Warnf("failed to parse message cache %s: %s", c.path, err)
	} else {
		// Count non-metadata entries and show a sample.
		msgEntries := 0
		sample := make([]string, 0, 5)
		for key := range c.data {
			if !strings.HasPrefix(key, lastSeenPrefix) && !strings.HasPrefix(key, deltaTokenPrefix) {
				msgEntries++
				if len(sample) < 5 {
					sample = append(sample, key)
				}
			}
		}
		c.logger.Infof("loaded %d entries from message cache %s (%d msg, %d metadata, sample: %v)",
			len(c.data), c.path, msgEntries, len(c.data)-msgEntries, sample)
	}
}

func (c *PersistentMsgCache) flushLoop() {
	for {
		select {
		case <-c.ticker.C:
			if time.Since(c.lastPrune) >= pruneInterval {
				c.prune()
			}
			c.Flush()
		case <-c.stopCh:
			c.ticker.Stop()
			c.Flush()
			return
		}
	}
}

// prune removes message ID entries older than maxAge.
// Metadata keys (__last_seen__, __delta_token__) are never pruned.
func (c *PersistentMsgCache) prune() {
	c.mu.Lock()
	defer c.mu.Unlock()
	cutoff := time.Now().Add(-c.maxAge)
	pruned := 0
	for key, entries := range c.data {
		if strings.HasPrefix(key, lastSeenPrefix) || strings.HasPrefix(key, deltaTokenPrefix) {
			continue
		}
		if len(entries) == 0 {
			delete(c.data, key)
			pruned++
			continue
		}
		// Use CreatedAt of first entry as the age of this mapping.
		// Zero time (old entries without CreatedAt) are pruned immediately.
		t := entries[0].CreatedAt
		if t.IsZero() || t.Before(cutoff) {
			delete(c.data, key)
			pruned++
		}
	}
	if pruned > 0 {
		c.dirty = true
		c.logger.Infof("pruned %d stale entries from message cache (older than %s)", pruned, c.maxAge)
	}
	c.lastPrune = time.Now()
}

// Add stores a message ID mapping. Sets CreatedAt on all entries.
func (c *PersistentMsgCache) Add(key string, entries []PersistentMsgEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for i := range entries {
		entries[i].CreatedAt = now
	}
	c.data[key] = entries
	c.dirty = true
}

// Get returns downstream IDs for a key, or nil if not found.
func (c *PersistentMsgCache) Get(key string) ([]PersistentMsgEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.data[key]
	return v, ok
}

// FindDownstream searches all entries for a downstream match (by ID field)
// and returns the canonical (upstream) key. This mirrors the linear scan
// in Gateway.FindCanonicalMsgID but over the persistent store.
func (c *PersistentMsgCache) FindDownstream(id string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, entries := range c.data {
		for _, entry := range entries {
			if entry.ID == id {
				return key
			}
		}
	}
	return ""
}

// Flush writes the cache to disk if it has been modified since the last flush.
func (c *PersistentMsgCache) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.dirty {
		return
	}
	// Count non-metadata entries for logging.
	msgEntries := 0
	for key := range c.data {
		if !strings.HasPrefix(key, lastSeenPrefix) && !strings.HasPrefix(key, deltaTokenPrefix) {
			msgEntries++
		}
	}
	data, err := json.MarshalIndent(c.data, "", "  ")
	if err != nil {
		c.logger.Errorf("failed to marshal message cache: %s", err)
		return
	}
	if err := os.WriteFile(c.path, data, 0600); err != nil {
		c.logger.Errorf("failed to write message cache %s: %s", c.path, err)
		return
	}
	c.dirty = false
	c.logger.Infof("flushed message cache %s (%d msg entries, %d total keys)", c.path, msgEntries, len(c.data))
}

// SetLastSeen stores the timestamp of the last processed message for a channel.
// The channelKey should uniquely identify a channel+account combination.
func (c *PersistentMsgCache) SetLastSeen(channelKey string, t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[lastSeenPrefix+channelKey] = []PersistentMsgEntry{{
		ID: t.Format(time.RFC3339Nano),
	}}
	c.dirty = true
}

// GetLastSeen returns the timestamp of the last processed message for a channel.
func (c *PersistentMsgCache) GetLastSeen(channelKey string) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, ok := c.data[lastSeenPrefix+channelKey]
	if !ok || len(entries) == 0 {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, entries[0].ID)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

const lastSeenPrefix = "__last_seen__:"
const deltaTokenPrefix = "__delta_token__:"

// SetDeltaToken stores a Graph API delta token for a channel.
// The channelKey should uniquely identify a channel+account combination.
func (c *PersistentMsgCache) SetDeltaToken(channelKey, token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[deltaTokenPrefix+channelKey] = []PersistentMsgEntry{{
		ID: token,
	}}
	c.dirty = true
}

// GetDeltaToken returns the stored Graph API delta token for a channel.
func (c *PersistentMsgCache) GetDeltaToken(channelKey string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, ok := c.data[deltaTokenPrefix+channelKey]
	if !ok || len(entries) == 0 {
		return "", false
	}
	return entries[0].ID, true
}

// Stop stops the background flush loop and performs a final flush.
func (c *PersistentMsgCache) Stop() {
	close(c.stopCh)
}
