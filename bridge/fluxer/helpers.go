package bfluxer

import (
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

var (
	// See https://discordapp.com/developers/docs/reference#message-formatting.
	channelMentionRE = regexp.MustCompile("<#[0-9]+>")
	userMentionRE    = regexp.MustCompile("@[^@\n]{1,32}")
	emoteRE          = regexp.MustCompile(`<a?(:\w+:)\d+>`)
)

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
