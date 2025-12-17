package bmatrix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"time"

	mautrix "maunium.net/go/mautrix"
	/* trunk-ignore(golangci-lint2/typecheck) */
	"maunium.net/go/mautrix/crypto"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func newMatrixUsername(username string) *matrixUsername {
	mUsername := new(matrixUsername)

	// check if we have a </tag>. if we have, we don't escape HTML. #696
	if htmlTag.MatchString(username) {
		mUsername.formatted = username
		// remove the HTML formatting for beautiful push messages #1188
		mUsername.plain = htmlReplacementTag.ReplaceAllString(username, "")
	} else {
		mUsername.formatted = html.EscapeString(username)
		mUsername.plain = username
	}

	return mUsername
}

// getRoomID retrieves a matching room ID from the channel name.
func (b *Bmatrix) getRoomID(channel string) id.RoomID {
	b.RLock()
	defer b.RUnlock()
	for ID, name := range b.RoomMap {
		if name == channel {
			return ID
		}
	}

	return ""
}

// getDisplayName retrieves the displayName for mxid, querying the homeserver if the mxid is not in the cache.
func (b *Bmatrix) getDisplayName(ctx context.Context, mxid id.UserID) string {
	// Localpart is the user name. Return it if UseUserName is set.
	if b.GetBool("UseUserName") {
		return mxid.Localpart()
	}

	b.RLock()

	if val, present := b.NicknameMap[mxid.Localpart()]; present {
		b.RUnlock()

		return val.displayName
	}

	b.RUnlock()

	resp, err := b.mc.GetDisplayName(ctx, mxid)
	if err != nil {
		b.Log.Errorf("Retrieving the display name for %s failed: %s", mxid, err)

		// Return the user name since retrieving the display name failed
		return b.cacheDisplayName(mxid, mxid.Localpart())
	}

	return b.cacheDisplayName(mxid, resp.DisplayName)
}

// cacheDisplayName stores the mapping between a mxid and a display name, to be reused later without performing a query to the homserver.
// Note that old entries are cleaned when this function is called.
func (b *Bmatrix) cacheDisplayName(mxid id.UserID, displayName string) string {
	now := time.Now()

	// scan to delete old entries, to stop memory usage from becoming too high with old entries.
	// In addition, we also detect if another user have the same username, and if so, we append their mxids to their usernames to differentiate them.
	toDelete := []string{}
	conflict := false

	b.Lock()

	for localpart, v := range b.NicknameMap {
		// to prevent username reuse across matrix servers - or even on the same server, append
		// the mxid to the username when there is a conflict
		if v.displayName == displayName {
			conflict = true
			// TODO: it would be nice to be able to rename previous messages from this user.
			// The current behavior is that only users with clashing usernames and *that have spoken since the bridge last started* will get their mxids shown, and I don't know if that's the expected behavior.
			v.displayName = fmt.Sprintf("%s (%s)", displayName, mxid)
			b.NicknameMap[localpart] = v
		}

		if now.Sub(v.lastUpdated) > 10*time.Minute {
			toDelete = append(toDelete, localpart)
		}
	}

	if conflict {
		displayName = fmt.Sprintf("%s (%s)", displayName, mxid)
	}

	for _, v := range toDelete {
		delete(b.NicknameMap, v)
	}

	b.NicknameMap[mxid.Localpart()] = NicknameCacheEntry{
		displayName: displayName,
		lastUpdated: now,
	}
	b.Unlock()

	return displayName
}

// handleError converts errors into httpError.
//
//nolint:exhaustivestruct
func handleError(err error) *httpError {
	var mErr mautrix.HTTPError
	if !errors.As(err, &mErr) {
		return &httpError{
			Err: "not a HTTPError",
		}
	}

	var httpErr httpError

	err = json.Unmarshal([]byte(mErr.ResponseBody), &httpErr)
	if err != nil {
		return &httpError{
			Err: "unmarshal failed",
		}
	}

	return &httpErr
}

func (b *Bmatrix) containsAttachment(content event.Content) bool {
	// Skip empty messages
	if content.AsMessage().MsgType == "" {
		return false
	}

	// Only allow image,video or file msgtypes
	if content.AsMessage().MsgType != event.MsgImage &&
		content.AsMessage().MsgType != event.MsgVideo &&
		content.AsMessage().MsgType != event.MsgFile {
		return false
	}

	return true
}

// getAvatarURL returns the avatar URL of the specified sender.
func (b *Bmatrix) getAvatarURL(ctx context.Context, sender id.UserID) string {
	urlPath, err := b.mc.GetAvatarURL(ctx, sender)
	if err != nil {
		b.Log.Errorf("getAvatarURL failed: %s", err)

		return ""
	}

	url := b.mc.BuildClientURL(urlPath)
	if url != "" {
		url += "?width=37&height=37&method=crop"
	}

	return url
}

// handleRatelimit handles the ratelimit errors and return if we're ratelimited and the amount of time to sleep
func (b *Bmatrix) handleRatelimit(err error) (time.Duration, bool) {
	httpErr := handleError(err)
	if httpErr.Errcode != "M_LIMIT_EXCEEDED" {
		return 0, false
	}

	b.Log.Debugf("ratelimited: %s", httpErr.Err)
	b.Log.Infof("getting ratelimited by matrix, sleeping approx %d seconds before retrying", httpErr.RetryAfterMs/1000)

	return time.Duration(httpErr.RetryAfterMs) * time.Millisecond, true
}

// retry function will check if we're ratelimited and retries again when backoff time expired
// returns original error if not 429 ratelimit
func (b *Bmatrix) retry(f func() error) error {
	b.rateMutex.Lock()
	defer b.rateMutex.Unlock()

	for {
		if err := f(); err != nil {
			if backoff, ok := b.handleRatelimit(err); ok {
				time.Sleep(backoff)
			} else {
				return err
			}
		} else {
			return nil
		}
	}
}

// Use this function to set up your client for E2EE
func setupEncryptedClientHelper(client *mautrix.Client, pickleKey []byte, sessionFile string) (*cryptohelper.CryptoHelper, error) {
	// The cryptohelper manages the creation of the correct CryptoStore and StateStore implementations.
	ch, err := cryptohelper.NewCryptoHelper(client, pickleKey, sessionFile)
	if err != nil {
		// This usually catches errors with opening the database file or key issues.
		return nil, fmt.Errorf("failed to create crypto helper: %w", err)
	}

	// Init MUST be called to load data from the database and prepare the internal machine.
	err = ch.Init(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to initialize crypto helper: %w", err)
	}

	// Hook the helper into the client
	client.Crypto = ch

	return ch, nil
}

// Verifies your recovery key with Matrix so you can message unimpeded
func verifyWithRecoveryKey(ctx context.Context, machine *crypto.OlmMachine, recoveryKey string) error {
	keyId, keyData, err := machine.SSSS.GetDefaultKeyData(ctx)
	if err != nil {
		return err
	}

	key, err := keyData.VerifyRecoveryKey(keyId, recoveryKey)
	if err != nil {
		return err
	}

	err = machine.FetchCrossSigningKeysFromSSSS(ctx, key)
	if err != nil {
		return err
	}

	err = machine.SignOwnDevice(ctx, machine.OwnIdentity())
	if err != nil {
		return err
	}

	err = machine.SignOwnMasterKey(ctx)

	return err
}
