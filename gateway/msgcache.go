package gateway

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// PersistentMsgEntry represents a single downstream message ID mapping.
type PersistentMsgEntry struct {
	Protocol   string `json:"protocol"`
	BridgeName string `json:"bridge_name"`
	ID         string `json:"id"`
	ChannelID  string `json:"channel_id"`
}

// PersistentMsgCache is a file-backed message ID cache that persists
// cross-bridge message ID mappings across restarts.
type PersistentMsgCache struct {
	mu     sync.Mutex
	path   string
	data   map[string][]PersistentMsgEntry
	dirty  bool
	ticker *time.Ticker
	stopCh chan struct{}
	logger *logrus.Entry
}

// NewPersistentMsgCache creates a new persistent cache backed by the given file path.
// Returns nil if path is empty. Loads existing data on creation and starts a
// background flush loop that writes changes to disk every 30 seconds.
func NewPersistentMsgCache(path string, logger *logrus.Entry) *PersistentMsgCache {
	if path == "" {
		return nil
	}
	c := &PersistentMsgCache{
		path:   path,
		data:   make(map[string][]PersistentMsgEntry),
		stopCh: make(chan struct{}),
		logger: logger,
	}
	c.load()
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
		c.logger.Infof("loaded %d entries from message cache %s", len(c.data), c.path)
	}
}

func (c *PersistentMsgCache) flushLoop() {
	for {
		select {
		case <-c.ticker.C:
			c.Flush()
		case <-c.stopCh:
			c.ticker.Stop()
			c.Flush()
			return
		}
	}
}

// Add stores a message ID mapping.
func (c *PersistentMsgCache) Add(key string, entries []PersistentMsgEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
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
}

// Stop stops the background flush loop and performs a final flush.
func (c *PersistentMsgCache) Stop() {
	close(c.stopCh)
}
