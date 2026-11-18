package helper

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const wave = "wave"

func TestSkinTone(t *testing.T) {
	for _, tt := range []struct {
		name string
		base string
		tone int
	}{
		{"+1", "+1", SkinToneNone},
		{"wave::skin-tone-2", wave, SkinToneLight},
		{"+1::skin-tone-6", "+1", SkinToneDark},
		{"wave::skin-tone-9", wave, SkinToneNone},
		{"wave::skin-tone-x", wave, SkinToneNone},
	} {
		base, tone := SplitSkinTone(tt.name)
		assert.Equal(t, tt.base, base, tt.name)
		assert.Equal(t, tt.tone, tone, tt.name)
	}

	assert.Equal(t, "wave::skin-tone-4", JoinSkinTone(wave, SkinToneMedium))
	assert.Equal(t, wave, JoinSkinTone(wave, SkinToneNone))
}

func TestReactionTracker(t *testing.T) {
	tr := NewReactionTracker(10)

	assert.True(t, tr.Add("m1", "+1", "alice"), "first reactor adds the reaction")
	assert.False(t, tr.Add("m1", "+1", "bob"), "second reactor: already there")
	assert.False(t, tr.Add("m1", "+1", "alice"), "duplicate")
	assert.True(t, tr.Add("m1", "heart", "bob"), "other emoji")
	assert.True(t, tr.Add("m2", "+1", "bob"), "other message")

	assert.False(t, tr.Remove("m1", "+1", "alice"), "bob still reacts")
	assert.True(t, tr.Remove("m1", "+1", "bob"), "last reactor removes the reaction")
	assert.True(t, tr.Remove("m1", "+1", "bob"), "unknown reactions are removed")
	assert.True(t, tr.Add("m1", "+1", "carol"), "can be added again")
}
