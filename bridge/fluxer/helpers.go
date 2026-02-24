package bfluxer

import (
	"regexp"

	"github.com/fluxergo/fluxergo/fluxer"
)

func (b *Bfluxer) getAllowedMentions() *fluxer.AllowedMentions {
	// If AllowMention is not specified, then allow all mentions (default Discord behavior)
	if !b.IsKeySet("AllowMention") {
		return nil
	}

	// Otherwise, allow only the mentions that are specified
	allowedMentionTypes := make([]fluxer.AllowedMentionType, 0, 3)
	for _, m := range b.GetStringSlice("AllowMention") {
		switch m {
		case "everyone":
			allowedMentionTypes = append(allowedMentionTypes, fluxer.AllowedMentionTypeEveryone)
		case "roles":
			allowedMentionTypes = append(allowedMentionTypes, fluxer.AllowedMentionTypeRoles)
		case "users":
			allowedMentionTypes = append(allowedMentionTypes, fluxer.AllowedMentionTypeUsers)
		}
	}

	return &fluxer.AllowedMentions{
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
