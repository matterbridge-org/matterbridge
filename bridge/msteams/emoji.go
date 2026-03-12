package bmsteams

import (
	"regexp"

	"github.com/kyokomi/emoji/v2"
)

// emojiMapping defines a regex-based emoji name replacement rule.
// This allows mapping platform-specific emoji shortcodes to a canonical form
// that the emoji library can resolve to unicode.
type emojiMapping struct {
	pattern *regexp.Regexp
	replace string
}

// emojiMappings contains all emoji name conversions.
// Add new entries here to handle additional platform differences.
var emojiMappings = []emojiMapping{
	// Mattermost flag emojis use hyphens (:flag-at:), standard format uses underscores (:flag_at:).
	{regexp.MustCompile(`:flag-([a-z]{2}):`), ":flag_$1:"},
}

// mapEmojis applies all emoji name mappings and then converts any resulting
// shortcodes to unicode. This catches platform-specific shortcodes that
// the gateway's initial emoji.Sprint() pass could not resolve.
func mapEmojis(text string) string {
	for _, m := range emojiMappings {
		text = m.pattern.ReplaceAllString(text, m.replace)
	}
	// Re-run emoji sprint for any newly mapped shortcodes.
	emoji.ReplacePadding = ""
	return emoji.Sprint(text)
}
