package bmattermost

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/matterbridge-org/matterbridge/bridge/helper"
	"github.com/matterbridge/matterclient"
	"github.com/mattermost/mattermost/server/public/model"
)

// reactionTrackerSize is the number of reactions whose remote users are
// remembered, see helper.ReactionTracker.
const reactionTrackerSize = 5000

// Mattermost emoji skin tone suffixes.
const (
	mmLightSkinTone       = "_light_skin_tone"
	mmMediumLightSkinTone = "_medium_light_skin_tone"
	mmMediumSkinTone      = "_medium_skin_tone"
	mmMediumDarkSkinTone  = "_medium_dark_skin_tone"
	mmDarkSkinTone        = "_dark_skin_tone"
)

// mattermostSkinTones lists the skin tone suffixes, longest first so that e.g.
// "_medium_light_skin_tone" isn't parsed as "_light_skin_tone".
var mattermostSkinTones = []struct {
	suffix string
	tone   int
}{
	{mmMediumLightSkinTone, helper.SkinToneMediumLight},
	{mmMediumDarkSkinTone, helper.SkinToneMediumDark},
	{mmMediumSkinTone, helper.SkinToneMedium},
	{mmLightSkinTone, helper.SkinToneLight},
	{mmDarkSkinTone, helper.SkinToneDark},
}

// emojiFromMattermost converts a Mattermost emoji name
// ("wave_light_skin_tone") to the format of config.Message reactions
// ("wave::skin-tone-2").
func emojiFromMattermost(name string) string {
	for _, st := range mattermostSkinTones {
		if base, ok := strings.CutSuffix(name, st.suffix); ok && base != "" {
			return helper.JoinSkinTone(base, st.tone)
		}
	}

	return name
}

// emojiToMattermost returns the Mattermost names to try for a reaction emoji
// ("wave::skin-tone-2"): with the skin tone first, then without it as not all
// emojis support skin tones.
func emojiToMattermost(emoji string) []string {
	base, tone := helper.SplitSkinTone(emoji)
	for _, st := range mattermostSkinTones {
		if st.tone == tone {
			return []string{base + st.suffix, base}
		}
	}

	return []string{base}
}

// handleReactionEvent converts a reaction_added/reaction_removed websocket
// event to a message for the gateway, or returns nil if it must be ignored.
func (b *Bmattermost) handleReactionEvent(message *matterclient.Message) *config.Message {
	raw, ok := message.Raw.GetData()["reaction"].(string)
	if !ok {
		return nil
	}

	var reaction model.Reaction

	err := json.Unmarshal([]byte(raw), &reaction)
	if err != nil {
		b.Log.Errorf("decoding reaction failed: %s", err)
		return nil
	}

	// ignore the reactions we relayed ourselves
	if b.mc.User == nil || reaction.UserId == b.mc.User.Id {
		return nil
	}

	// the channel ID is not set in the reaction on removal
	channelID := message.Raw.GetBroadcast().ChannelId
	if channelID == "" {
		channelID = reaction.ChannelId
	}

	if teamID := b.mc.GetChannelTeamID(channelID); teamID != "" && teamID != b.TeamID {
		b.Log.Debug("reaction from other team, ignoring")
		return nil
	}

	channelName := b.getChannelName(channelID)
	if channelName == "" {
		channelName = b.mc.GetChannelName(channelID)
	}

	event := config.EventReactionAdd
	if message.Raw.EventType() == model.WebsocketEventReactionRemoved {
		event = config.EventReactionRemove
	}

	username := b.mc.GetUserName(reaction.UserId)
	if !b.GetBool("useusername") {
		if nick := b.mc.GetNickName(reaction.UserId); nick != "" {
			username = nick
		}
	}

	return &config.Message{
		Event:    event,
		ID:       reaction.PostId,
		Text:     emojiFromMattermost(reaction.EmojiName),
		Channel:  channelName,
		Username: username,
		UserID:   reaction.UserId,
		Extra:    make(map[string][]any),
	}
}

// sendReaction adds or removes, as our user, the reaction of a remote user.
func (b *Bmattermost) sendReaction(msg *config.Message) (string, error) {
	if b.mc == nil || b.mc.User == nil || msg.ID == "" || msg.Text == "" {
		return "", nil
	}

	reactor := msg.Account + " " + msg.UserID
	if msg.UserID == "" {
		reactor = msg.Account + " " + msg.Username
	}

	names := emojiToMattermost(msg.Text)

	if msg.Event == config.EventReactionRemove {
		if !b.reactions.Remove(msg.ID, msg.Text, reactor) {
			return "", nil
		}

		for _, name := range names {
			_, err := b.mc.Client.DeleteReaction(context.TODO(), &model.Reaction{UserId: b.mc.User.Id, PostId: msg.ID, EmojiName: name})
			if err != nil {
				b.Log.Debugf("removing reaction %s failed: %s", name, err)
			}
		}

		return "", nil
	}

	if !b.reactions.Add(msg.ID, msg.Text, reactor) {
		return "", nil
	}

	var err error
	for _, name := range names {
		_, _, err = b.mc.Client.SaveReaction(context.TODO(), &model.Reaction{UserId: b.mc.User.Id, PostId: msg.ID, EmojiName: name})
		if err == nil {
			return "", nil
		}
	}

	return "", fmt.Errorf("adding reaction %s failed: %w", msg.Text, err)
}
