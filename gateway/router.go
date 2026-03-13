package gateway

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/matterbridge-org/matterbridge/bridge"
	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/matterbridge-org/matterbridge/gateway/samechannel"
	"github.com/sirupsen/logrus"
)

type Router struct {
	config.Config
	sync.RWMutex

	BridgeMap        map[string]bridge.Factory
	Gateways         map[string]*Gateway
	Message          chan config.Message
	MattermostPlugin chan config.Message

	logger *logrus.Entry
}

// NewRouter initializes a new Matterbridge router for the specified configuration and
// sets up all required gateways.
func NewRouter(rootLogger *logrus.Logger, cfg config.Config, bridgeMap map[string]bridge.Factory) (*Router, error) {
	logger := rootLogger.WithFields(logrus.Fields{"prefix": "router"})

	r := &Router{
		Config:           cfg,
		BridgeMap:        bridgeMap,
		Message:          make(chan config.Message),
		MattermostPlugin: make(chan config.Message),
		Gateways:         make(map[string]*Gateway),
		logger:           logger,
	}
	sgw := samechannel.New(cfg)
	gwconfigs := append(sgw.GetConfig(), cfg.BridgeValues().Gateway...)

	for idx := range gwconfigs {
		entry := &gwconfigs[idx]
		if !entry.Enable {
			continue
		}
		if entry.Name == "" {
			return nil, fmt.Errorf("%s", "Gateway without name found")
		}
		if _, ok := r.Gateways[entry.Name]; ok {
			return nil, fmt.Errorf("Gateway with name %s already exists", entry.Name)
		}
		r.Gateways[entry.Name] = New(rootLogger, entry, r)
	}
	return r, nil
}

// Start will connect all gateways belonging to this router and subsequently route messages
// between them.
func (r *Router) Start() error {
	// Deprecating MediaServerUpload. Remove in future v2.1 release
	deprecatedValue, _ := r.GetString("MediaServerUpload")
	if deprecatedValue != "" {
		r.logger.Fatal("MediaServerUpload config option has been deprecated. You should either remove this option from your configuration, or help us document it.")
	}

	m := make(map[string]*bridge.Bridge)
	if len(r.Gateways) == 0 {
		return fmt.Errorf("no [[gateway]] configured. See https://github.com/42wim/matterbridge/wiki/How-to-create-your-config for more info")
	}
	for _, gw := range r.Gateways {
		r.logger.Infof("Parsing gateway %s", gw.Name)
		if len(gw.Bridges) == 0 {
			return fmt.Errorf("no bridges configured for gateway %s. See https://github.com/42wim/matterbridge/wiki/How-to-create-your-config for more info", gw.Name)
		}
		for _, br := range gw.Bridges {
			m[br.Account] = br
		}
	}
	for _, br := range m {
		r.logger.Infof("Starting bridge: %s ", br.Account)
		err := br.Connect()
		if err != nil {
			e := fmt.Errorf("Bridge %s failed to start: %v", br.Account, err)
			if r.disableBridge(br, e) {
				continue
			}
			return e
		}
		err = br.JoinChannels()
		if err != nil {
			e := fmt.Errorf("Bridge %s failed to join channel: %v", br.Account, err)
			if r.disableBridge(br, e) {
				continue
			}
			return e
		}
	}
	// remove unused bridges
	for _, gw := range r.Gateways {
		for i, br := range gw.Bridges {
			if br.Bridger == nil {
				r.logger.Errorf("removing failed bridge %s", i)
				delete(gw.Bridges, i)
			}
		}
	}
	go r.handleReceive()
	//go r.updateChannelMembers()
	return nil
}

// Stop performs a graceful shutdown: flushes and stops all persistent caches.
func (r *Router) Stop() {
	for _, gw := range r.Gateways {
		gw.stopPersistentCaches()
	}
}

// disableBridge returns true and empties a bridge if we have IgnoreFailureOnStart configured
// otherwise returns false
func (r *Router) disableBridge(br *bridge.Bridge, err error) bool {
	if r.BridgeValues().General.IgnoreFailureOnStart {
		r.logger.Error(err)
		// setting this bridge empty
		*br = bridge.Bridge{
			Log: br.Log,
		}
		return true
	}
	return false
}

func (r *Router) getBridge(account string) *bridge.Bridge {
	for _, gw := range r.Gateways {
		if br, ok := gw.Bridges[account]; ok {
			return br
		}
	}
	return nil
}

func (r *Router) handleReceive() {
	for msg := range r.Message {
		msg := msg // scopelint
		r.handleEventGetChannelMembers(&msg)
		r.handleEventFailure(&msg)
		r.handleEventRejoinChannels(&msg)

		// Set message protocol based on the account it came from
		msg.Protocol = r.getBridge(msg.Account).Protocol

		// Handle historical cache population events — don't relay, just cache.
		if msg.Event == config.EventHistoricalMapping {
			r.handleHistoricalMapping(&msg)
			continue
		}

		// Handle replay messages — check persistent cache for dedup, then treat as normal.
		isReplay := msg.Event == config.EventReplayMessage
		if isReplay {
			if msg.ID != "" {
				cacheKey := msg.Protocol + " " + msg.ID
				r.logger.Debugf("replay: dedup check for %s (account=%s)", cacheKey, msg.Account)
				alreadyBridged := false
				for _, gw := range r.Gateways {
					if !gw.hasPersistentCache() {
						r.logger.Debugf("replay: gateway %s has no persistent cache", gw.Name)
						continue
					}
					if _, exists := gw.persistentCacheGet(cacheKey); exists {
						alreadyBridged = true
						break
					}
					if downstream := gw.persistentCacheFindDownstream(cacheKey); downstream != "" {
						alreadyBridged = true
						break
					}
				}
				if alreadyBridged {
					r.logger.Debugf("replay: skipping already-bridged message %s", cacheKey)
					continue
				}
				r.logger.Debugf("replay: message %s NOT found in cache, will bridge", cacheKey)
			}
			msg.Event = "" // clear so downstream pipeline treats it as a normal message
		}

		filesHandled := false
		for _, gw := range r.Gateways {
			// record all the message ID's of the different bridges
			var msgIDs []*BrMsgID
			if gw.ignoreMessage(&msg) {
				continue
			}
			msg.Timestamp = time.Now()
			gw.modifyMessage(&msg)
			if !filesHandled {
				gw.handleFiles(&msg)
				filesHandled = true
			}
			for _, br := range gw.Bridges {
				msgIDs = append(msgIDs, gw.handleMessage(&msg, br)...)
			}

			if msg.ID != "" {
				cacheKey := msg.Protocol + " " + msg.ID
				_, exists := gw.Messages.Get(cacheKey)

				// Only add the message ID if it doesn't already exist
				//
				// For some bridges we always add/update the message ID.
				// This is necessary as msgIDs will change if a bridge returns
				// a different ID in response to edits.
				if !exists {
					gw.Messages.Add(cacheKey, msgIDs)
				}

				// Write-through to persistent cache.
				if gw.hasPersistentCache() && len(msgIDs) > 0 {
					var entries []PersistentMsgEntry
					for _, mid := range msgIDs {
						if mid.br != nil && mid.ID != "" {
							entries = append(entries, PersistentMsgEntry{
								Protocol:   mid.br.Protocol,
								BridgeName: mid.br.Name,
								ID:         mid.ID,
								ChannelID:  mid.ChannelID,
							})
						}
					}
					if len(entries) > 0 {
						gw.persistentCacheAdd(cacheKey, entries, msg.Account)
					} else if isReplay {
						r.logger.Debugf("replay: no cacheable entries for %s (msgIDs=%d)", cacheKey, len(msgIDs))
					}
					// Update last-seen timestamp for the source channel.
					channelKey := msg.Channel + msg.Account
					if cache, ok := gw.BridgeCaches[msg.Account]; ok && cache != nil {
						cache.SetLastSeen(channelKey, msg.Timestamp)
					}
				}
			}
		}
	}
}

// handleHistoricalMapping processes historical ID mapping events from bridges.
// It extracts the source-ID marker and stores a bidirectional mapping in the
// persistent cache of every gateway that has both the reporting bridge and
// the source bridge configured.
func (r *Router) handleHistoricalMapping(msg *config.Message) {
	if msg.ID == "" || msg.Extra == nil {
		return
	}
	srcIDs, ok := msg.Extra["source_msgid"]
	if !ok || len(srcIDs) == 0 {
		return
	}
	sourceIDStr, ok := srcIDs[0].(string)
	if !ok || sourceIDStr == "" {
		return
	}

	// Parse "protocol:messageID" from the source marker.
	parts := strings.SplitN(sourceIDStr, ":", 2)
	if len(parts) != 2 {
		return
	}
	sourceProtocol := parts[0]
	sourceMessageID := parts[1]

	localKey := msg.Protocol + " " + msg.ID
	sourceKey := sourceProtocol + " " + sourceMessageID

	for _, gw := range r.Gateways {
		if !gw.hasPersistentCache() {
			continue
		}

		// Find the local bridge (the one that reported this mapping).
		localBridge := gw.findBridge(msg.Protocol, extractBridgeName(msg.Account))
		if localBridge == nil {
			continue
		}

		// Find a bridge matching the source protocol in this gateway.
		var sourceBridge *bridge.Bridge
		for _, br := range gw.Bridges {
			if br.Protocol == sourceProtocol {
				sourceBridge = br
				break
			}
		}
		if sourceBridge == nil {
			continue
		}

		// Find channel IDs for both sides.
		localChannelID := msg.Channel + msg.Account
		var sourceChannelID string
		for chID, ch := range gw.Channels {
			if ch.Account == sourceBridge.Account {
				sourceChannelID = chID
				break
			}
		}

		// Store: sourceKey → points to local bridge (e.g., "mattermost POST123" → msteams entry)
		if _, exists := gw.persistentCacheGet(sourceKey); !exists {
			gw.persistentCacheAdd(sourceKey, []PersistentMsgEntry{{
				Protocol:   localBridge.Protocol,
				BridgeName: localBridge.Name,
				ID:         localKey,
				ChannelID:  localChannelID,
			}}, sourceBridge.Account)
		}

		// Store: localKey → points to source bridge (e.g., "msteams TEAMS456" → mattermost entry)
		if _, exists := gw.persistentCacheGet(localKey); !exists && sourceChannelID != "" {
			gw.persistentCacheAdd(localKey, []PersistentMsgEntry{{
				Protocol:   sourceBridge.Protocol,
				BridgeName: sourceBridge.Name,
				ID:         sourceKey,
				ChannelID:  sourceChannelID,
			}}, msg.Account)
		}
	}
}

// extractBridgeName returns the part after the dot in an account string like "msteams.windoof".
func extractBridgeName(account string) string {
	parts := strings.SplitN(account, ".", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return account
}

// updateChannelMembers sends every minute an GetChannelMembers event to all bridges.
func (r *Router) updateChannelMembers() {
	// TODO sleep a minute because slack can take a while
	// fix this by having actually connectionDone events send to the router
	time.Sleep(time.Minute)
	for {
		for _, gw := range r.Gateways {
			for _, br := range gw.Bridges {
				// only for slack now
				if br.Protocol != "slack" {
					continue
				}
				r.logger.Debugf("sending %s to %s", config.EventGetChannelMembers, br.Account)
				if _, err := br.Send(config.Message{Event: config.EventGetChannelMembers}); err != nil {
					r.logger.Errorf("updateChannelMembers: %s", err)
				}
			}
		}
		time.Sleep(time.Minute)
	}
}
