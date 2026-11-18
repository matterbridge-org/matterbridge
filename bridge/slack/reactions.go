package bslack

import (
	"fmt"

	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/slack-go/slack"
)

// relayReaction sends a reaction added to (or removed from) a message to the
// gateway. Slack emoji names ("+1", "wave::skin-tone-2") already are in the
// format of config.Message reactions.
func (b *Bslack) relayReaction(messages chan *config.Message, user, reaction, itemType, channel, ts string, added bool) {
	// only reactions on messages, not on files, and not our own reactions
	if itemType != "message" || user == "" || user == b.actingUserID {
		return
	}

	channelInfo, err := b.channels.getChannelByID(channel)
	if err != nil {
		b.Log.Debugf("reaction in unknown channel %s: %s", channel, err)
		return
	}

	event := config.EventReactionAdd
	if !added {
		event = config.EventReactionRemove
	}

	rmsg := &config.Message{
		Event:    event,
		ID:       ts,
		Text:     reaction,
		Channel:  channelInfo.Name,
		Account:  b.Account,
		Protocol: b.Protocol,
		UserID:   user,
		Username: b.users.getUsername(user),
		Extra:    make(map[string][]any),
	}
	if b.useChannelID {
		rmsg.Channel = "ID:" + channelInfo.ID
	}

	b.Log.Debugf("<= Sending reaction %s from %s on %s to gateway", event, rmsg.Username, b.Account)

	messages <- rmsg
}

// sendReaction adds or removes, as our user, the reaction of a remote user.
func (b *Bslack) sendReaction(msg *config.Message, channelID string) error {
	if msg.ID == "" || msg.Text == "" {
		return nil
	}

	reactor := msg.Account + " " + msg.UserID
	if msg.UserID == "" {
		reactor = msg.Account + " " + msg.Username
	}

	ref := slack.NewRefToMessage(channelID, msg.ID)

	if msg.Event == config.EventReactionRemove {
		if !b.reactions.Remove(msg.ID, msg.Text, reactor) {
			return nil
		}

		err := b.sc.RemoveReaction(msg.Text, ref)
		if err != nil && err.Error() != "no_reaction" {
			return fmt.Errorf("removing reaction %s failed: %w", msg.Text, err)
		}

		return nil
	}

	if !b.reactions.Add(msg.ID, msg.Text, reactor) {
		return nil
	}

	err := b.sc.AddReaction(msg.Text, ref)
	if err != nil && err.Error() != "already_reacted" {
		return fmt.Errorf("adding reaction %s failed: %w", msg.Text, err)
	}

	return nil
}
