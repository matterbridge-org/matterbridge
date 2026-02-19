package bdiscord

import (
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
)

func TestHandleEmbed(t *testing.T) {
	markedTestCases := map[string]struct {
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
			result: "\nembed:\nblah\n",
		},
		"two": {
			embed: &discordgo.MessageEmbed{
				Title:       "blah",
				Description: "blah2",
			},
			result: "\nembed:\nblah\nblah2\n",
		},
		"three": {
			embed: &discordgo.MessageEmbed{
				Title:       "blah",
				Description: "blah2",
				URL:         "blah3",
			},
			result: "\nembed:\nblah\nblah2\nblah3\n",
		},
		"twob": {
			embed: &discordgo.MessageEmbed{
				Description: "blah2",
				URL:         "blah3",
			},
			result: "\nembed:\nblah2\nblah3\n",
		},
		"oneb": {
			embed: &discordgo.MessageEmbed{
				URL: "blah3",
			},
			result: "\nembed:\nblah3\n",
		},
	}

	unmarkedTestCases := map[string]struct {
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

	for name, tc := range markedTestCases {
		assert.Equalf(t, tc.result, handleEmbed(tc.embed, false), "Testcases %s", name)
	}

	for name, tc := range unmarkedTestCases {
		assert.Equalf(t, tc.result, handleEmbed(tc.embed, true), "Testcases %s", name)
	}
}
