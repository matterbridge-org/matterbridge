package helper

import (
	"strconv"
	"strings"
	"sync"

	lru "github.com/hashicorp/golang-lru/v2"
)

// skinToneSeparator separates an emoji name from its skin tone in the format
// used for reactions in config.Message (Slack's): "wave::skin-tone-2".
const skinToneSeparator = "::skin-tone-"

// Skin tones, as numbered by Slack (Fitzpatrick scale types 1-2 to 6).
const (
	SkinToneNone        = 0
	SkinToneLight       = 2
	SkinToneMediumLight = 3
	SkinToneMedium      = 4
	SkinToneMediumDark  = 5
	SkinToneDark        = 6
)

// SplitSkinTone splits a reaction emoji name ("wave::skin-tone-2") into its
// base name and skin tone (SkinToneNone when there is none).
func SplitSkinTone(name string) (string, int) {
	base, tone, found := strings.Cut(name, skinToneSeparator)
	if !found {
		return name, SkinToneNone
	}

	n, err := strconv.Atoi(tone)
	if err != nil || n < SkinToneLight || n > SkinToneDark {
		return base, SkinToneNone
	}

	return base, n
}

// JoinSkinTone returns the reaction emoji name for base with the skin tone.
func JoinSkinTone(base string, tone int) string {
	if tone < SkinToneLight || tone > SkinToneDark {
		return base
	}

	return base + skinToneSeparator + strconv.Itoa(tone)
}

// ReactionTracker counts the remote users reacting with the same emoji on the
// same message.
//
// A bridge relays the reactions of remote users as its own user, which can
// only react once with an emoji on a message: the reaction must only be added
// for the first remote user, and removed when the last one removes it.
type ReactionTracker struct {
	mu        sync.Mutex
	reactions *lru.Cache[string, map[string]struct{}]
}

// NewReactionTracker returns a ReactionTracker remembering the reactors of
// the last size reactions.
func NewReactionTracker(size int) *ReactionTracker {
	cache, err := lru.New[string, map[string]struct{}](size)
	if err != nil {
		panic(err)
	}

	return &ReactionTracker{reactions: cache}
}

// Add records that reactor reacted with emoji on msgID. It returns true if
// the reaction must be added, i.e. reactor is the first one.
func (t *ReactionTracker) Add(msgID, emoji, reactor string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	key := msgID + " " + emoji

	reactors, ok := t.reactions.Get(key)
	if !ok {
		reactors = make(map[string]struct{})
		t.reactions.Add(key, reactors)
	}

	if _, dup := reactors[reactor]; dup {
		return false
	}

	reactors[reactor] = struct{}{}

	return len(reactors) == 1
}

// Remove records that reactor removed its emoji reaction on msgID. It returns
// true if the reaction must be removed, i.e. no other reactor is left (or the
// reaction is unknown, e.g. it was added before a restart).
func (t *ReactionTracker) Remove(msgID, emoji, reactor string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	key := msgID + " " + emoji

	reactors, ok := t.reactions.Get(key)
	if !ok {
		return true
	}

	delete(reactors, reactor)

	if len(reactors) > 0 {
		return false
	}

	t.reactions.Remove(key)

	return true
}
