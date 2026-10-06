package bfluxer

import (
	"context"
	"regexp"

	"github.com/fluxer-flo/flo"
)

func (b *Bfluxer) getUGCUrl() string {
	ugcUrl := b.GetString("UGCUrl")
	if ugcUrl != "" {
		return ugcUrl
	}

	return "https://fluxerusercontent.com"
}

func (b *Bfluxer) getAllowedMentions() *flo.AllowedMentions {
	// If AllowMention is not specified, then all mentions are disabled
	if !b.IsKeySet("AllowMention") {
		return nil
	}

	// Otherwise, allow only the mentions that are specified
	allowedMentionTypes := make([]flo.AllowedMentionsParse, 0, 3)

	for _, m := range b.GetStringSlice("AllowMention") {
		switch m {
		case "everyone":
			allowedMentionTypes = append(allowedMentionTypes, flo.AllowedMentionsParseEveryone)
		case "roles":
			allowedMentionTypes = append(allowedMentionTypes, flo.AllowedMentionsParseRoles)
		case "users":
			allowedMentionTypes = append(allowedMentionTypes, flo.AllowedMentionsParseUsers)
		}
	}

	return &flo.AllowedMentions{
		Parse: allowedMentionTypes,
	}
}

func (b *Bfluxer) getGuild(id flo.ID) (flo.Guild, bool) {
	guild, ok := b.cache.Guilds.Get(id)
	if !ok {
		var err error

		guild, err = b.rest.GetGuild(context.TODO(), id)
		if err != nil {
			return flo.Guild{}, false
		}

		return guild, true
	}

	return guild, true
}

func (b *Bfluxer) getChannelName(id flo.ID) string {
	guild, ok := b.getGuild(b.guildID)
	if !ok {
		return ""
	}

	// should be automatically updated when guild updates..
	channel, ok := guild.Channels.Get(id)
	if !ok {
		return ""
	}

	return *channel.Name
}

var (
	// See https://discordapp.com/developers/docs/reference#message-formatting.
	channelMentionRE = regexp.MustCompile("<#[0-9]+>")
	emoteRE          = regexp.MustCompile(`<a?(:\w+:)\d+>`)
)

func (b *Bfluxer) replaceChannelMentions(text string) string {
	replaceChannelMentionFunc := func(match string) string {
		channelID, err := flo.ParseID(match[2 : len(match)-1])
		if err != nil {
			return "#unknownchannel"
		}

		channelName := b.getChannelName(channelID)
		if channelName == "" {
			return "#unknownchannel"
		}

		return "#" + channelName
	}

	return channelMentionRE.ReplaceAllStringFunc(text, replaceChannelMentionFunc)
}

func replaceEmotes(text string) string {
	return emoteRE.ReplaceAllString(text, "$1")
}

func (b *Bfluxer) replaceAction(text string) (string, bool) {
	length := len(text)
	if length > 1 && text[0] == '_' && text[length-1] == '_' {
		return text[1 : length-1], true
	}

	return text, false
}
