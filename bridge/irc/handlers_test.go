package birc

import (
	"testing"

	"github.com/lrstanley/girc"
)

func TestFormatJoinLeaveText(t *testing.T) {
	src := func(name, ident, host string) *girc.Source {
		return &girc.Source{Name: name, Ident: ident, Host: host}
	}

	cases := []struct {
		name    string
		event   girc.Event
		verbose bool
		want    string
	}{
		{
			name:  "join",
			event: girc.Event{Source: src("alice", "alice", "host"), Command: "JOIN", Params: []string{"#chan"}},
			want:  "alice joins",
		},
		{
			name:  "part",
			event: girc.Event{Source: src("alice", "alice", "host"), Command: "PART", Params: []string{"#chan"}},
			want:  "alice parts",
		},
		{
			name:  "quit",
			event: girc.Event{Source: src("alice", "alice", "host"), Command: "QUIT", Params: []string{"Bye"}},
			want:  "alice quits",
		},
		{
			name:  "kick with reason",
			event: girc.Event{Source: src("vjt", "vjt", "host"), Command: "KICK", Params: []string{"#chan", "alice", "spam"}},
			want:  "vjt kicked alice (spam)",
		},
		{
			name:  "kick without reason",
			event: girc.Event{Source: src("vjt", "vjt", "host"), Command: "KICK", Params: []string{"#chan", "alice"}},
			want:  "vjt kicked alice",
		},
		{
			name:    "verbose join",
			event:   girc.Event{Source: src("alice", "alice", "host.example"), Command: "JOIN", Params: []string{"#chan"}},
			verbose: true,
			want:    "alice (alice@host.example) joins",
		},
		{
			name:    "verbose kick",
			event:   girc.Event{Source: src("vjt", "vjt", "host.example"), Command: "KICK", Params: []string{"#chan", "alice", "bye"}},
			verbose: true,
			want:    "vjt (vjt@host.example) kicked alice (bye)",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := formatJoinLeaveText(c.event, c.verbose)
			if got != c.want {
				t.Errorf("formatJoinLeaveText() = %q, want %q", got, c.want)
			}
		})
	}
}
