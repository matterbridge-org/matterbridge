package bmattermost

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEmojiMattermost(t *testing.T) {
	for mm, canonical := range map[string]string{
		"+1":                          "+1",
		"wave_light_skin_tone":        "wave::skin-tone-2",
		"wave_medium_light_skin_tone": "wave::skin-tone-3",
		"thumbsup_medium_skin_tone":   "thumbsup::skin-tone-4",
		"wave_medium_dark_skin_tone":  "wave::skin-tone-5",
		"wave_dark_skin_tone":         "wave::skin-tone-6",
		mmDarkSkinTone:                mmDarkSkinTone,
	} {
		assert.Equal(t, canonical, emojiFromMattermost(mm), mm)
	}

	assert.Equal(t, []string{"+1"}, emojiToMattermost("+1"))
	assert.Equal(t, []string{"wave_medium_light_skin_tone", "wave"}, emojiToMattermost("wave::skin-tone-3"))
	assert.Equal(t, []string{"+1_dark_skin_tone", "+1"}, emojiToMattermost("+1::skin-tone-6"))
}
