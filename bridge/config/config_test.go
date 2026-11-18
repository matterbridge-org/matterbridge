package config

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
)

func TestGetStringSlice2D(t *testing.T) {
	logger, hook := test.NewNullLogger()
	logger.SetLevel(logrus.DebugLevel)

	cfg := NewConfigFromString(logger, []byte(`
[general]
ReplaceNicks=[["foo","bar"],["a","b"]]
ExtractNicks="not an array"
`))

	// An optional key that is not configured is not an error and must not
	// spam the logs on every message.
	res, ok := cfg.GetStringSlice2D("general.ReplaceMessages")
	assert.False(t, ok)
	assert.Nil(t, res)
	assert.Empty(t, hook.AllEntries())

	res, ok = cfg.GetStringSlice2D("general.ReplaceNicks")
	assert.True(t, ok)
	assert.Equal(t, [][]string{{"foo", "bar"}, {"a", "b"}}, res)
	assert.Empty(t, hook.AllEntries())

	// A key with a wrong type is still reported.
	_, ok = cfg.GetStringSlice2D("general.ExtractNicks")
	assert.False(t, ok)

	if assert.Len(t, hook.AllEntries(), 1) {
		assert.Equal(t, logrus.ErrorLevel, hook.LastEntry().Level)
	}
}
