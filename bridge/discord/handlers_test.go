package bdiscord

import (
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
)

func TestHandleEmbed(t *testing.T) {
	testcases := map[string]struct {
		embed  *discordgo.MessageEmbed
		result string
	}{
		"allempty": {
			embed:  &discordgo.MessageEmbed{},
			result: "",
		},
		"one": {
			embed: &discordgo.MessageEmbed{
				Title: "blah",
			},
			result: "\nblah\n",
		},
		"two": {
			embed: &discordgo.MessageEmbed{
				Title:       "blah",
				Description: "blah2",
			},
			result: "\nblah\nblah2\n",
		},
		"three": {
			embed: &discordgo.MessageEmbed{
				Title:       "blah",
				Description: "blah2",
				URL:         "blah3",
			},
			result: "\nblah\nblah2\nblah3\n",
		},
		"twob": {
			embed: &discordgo.MessageEmbed{
				Description: "blah2",
				URL:         "blah3",
			},
			result: "\nblah2\nblah3\n",
		},
		"oneb": {
			embed: &discordgo.MessageEmbed{
				URL: "blah3",
			},
			result: "\nblah3\n",
		},
	}

	for name, tc := range testcases {
		assert.Equalf(t, tc.result, handleEmbed(tc.embed), "Testcases %s", name)
	}
}
