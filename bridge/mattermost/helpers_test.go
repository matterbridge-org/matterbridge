package bmattermost

import (
	"testing"

	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/stretchr/testify/assert"
)

func TestFileComments(t *testing.T) {
	data := []byte("x")
	msgWith := func(comments ...string) *config.Message {
		msg := &config.Message{Extra: make(map[string][]any)}
		for _, c := range comments {
			msg.Extra["file"] = append(msg.Extra["file"], config.FileInfo{Name: "f.png", Data: &data, Comment: c})
		}

		return msg
	}

	// Slack: the text is moved to the comment of the first file only.
	assert.Equal(t, "look at this", fileComments(msgWith("look at this", "", "")))
	// Mattermost: the text is copied to the comment of every file.
	assert.Equal(t, "same", fileComments(msgWith("same", "same")))
	// Different captions are all kept.
	assert.Equal(t, "one\ntwo", fileComments(msgWith("one", "two")))
	assert.Equal(t, "", fileComments(msgWith("", "")))
	assert.Equal(t, "", fileComments(&config.Message{}))
}

func TestPrefixNick(t *testing.T) {
	nick := "**alice** (slack): "

	tests := map[string]string{
		"hello":                 "**alice** (slack): hello",
		"":                      "**alice** (slack): ",
		"```\ncode\n```":        "**alice** (slack):\n```\ncode\n```",
		"> quoted":              "**alice** (slack):\n> quoted",
		"- item":                "**alice** (slack):\n- item",
		"1. first":              "**alice** (slack):\n1. first",
		"## Title":              "**alice** (slack):\n## Title",
		"| a | b |":             "**alice** (slack):\n| a | b |",
		"#hashtag is not title": "**alice** (slack): #hashtag is not title",
		"-1 is a number":        "**alice** (slack): -1 is a number",
	}
	for text, want := range tests {
		assert.Equal(t, want, prefixNick(nick, text), "text %q", text)
	}

	assert.Equal(t, "hello", prefixNick("", "hello"))
}
