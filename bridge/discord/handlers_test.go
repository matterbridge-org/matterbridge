package bdiscord

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
)

func handleEmbed(embed *discordgo.MessageEmbed) string {
	var result string

	result = " embed: {TITLE} - {DESCRIPTION} - {URL}"

	result = strings.ReplaceAll(result, "{URL}", embed.URL)
	result = strings.ReplaceAll(result, "{TITLE}", embed.Title)
	result = strings.ReplaceAll(result, "{DESCRIPTION}", embed.Description)
	result = strings.ReplaceAll(result, "{TIME}", embed.Timestamp)
	// todo: make these work without making the entire thing panic
	// result = strings.ReplaceAll(result, "{FOOTER}", embed.Footer.Text)
	// result = strings.ReplaceAll(result, "{IMAGE}", embed.Image.URL)
	// result = strings.ReplaceAll(result, "{THUMBNAIL}", embed.Thumbnail.URL)
	// result = strings.ReplaceAll(result, "{VIDEO}", embed.Video.URL)
	// result = strings.ReplaceAll(result, "{PROVIDER}", embed.Provider.URL)
	// result = strings.ReplaceAll(result, "{AUTHOR}", embed.Author.Name)
	// result = strings.ReplaceAll(result, "{AUTHORURL}", embed.Author.URL)

	if result != "" {
		result += "\n"
	}

	return result
}

func TestHandleEmbed(t *testing.T) {
	testcases := map[string]struct {
		embed  *discordgo.MessageEmbed
		result string
	}{
		"allempty": {
			embed:  &discordgo.MessageEmbed{},
			result: " embed:  -  - \n",
		},
		"one": {
			embed: &discordgo.MessageEmbed{
				Title: "blah",
			},
			result: " embed: blah -  - \n",
		},
		"two": {
			embed: &discordgo.MessageEmbed{
				Title:       "blah",
				Description: "blah2",
			},
			result: " embed: blah - blah2 - \n",
		},
		"three": {
			embed: &discordgo.MessageEmbed{
				Title:       "blah",
				Description: "blah2",
				URL:         "blah3",
			},
			result: " embed: blah - blah2 - blah3\n",
		},
		"twob": {
			embed: &discordgo.MessageEmbed{
				Description: "blah2",
				URL:         "blah3",
			},
			result: " embed:  - blah2 - blah3\n",
		},
		"oneb": {
			embed: &discordgo.MessageEmbed{
				URL: "blah3",
			},
			result: " embed:  -  - blah3\n",
		},
	}

	for name, tc := range testcases {
		assert.Equalf(t, tc.result, handleEmbed(tc.embed), "Testcases %s", name)
	}
}
