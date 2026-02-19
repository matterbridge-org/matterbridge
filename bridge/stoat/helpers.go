package bstoat

import (
	"github.com/sentinelb51/revoltgo"
)

// Get a Channel from either the cache or the API.
func (b *Bstoat) getChannel(cID string) *revoltgo.Channel {
	if channel := b.session.State.Channel(cID); channel != nil {
		return channel
	}
	channel, _ := b.session.Channel(cID)
	return channel
}

// Get a ServerMember from either the cache or the API.
func (b *Bstoat) getMember(uID, sID string) *revoltgo.ServerMember {
	if member := b.session.State.Member(uID, sID); member != nil {
		return member
	}
	member, _ := b.session.ServerMember(sID, uID)
	return member
}

// Get a User from either the cache or the API.
func (b *Bstoat) getUser(uID string) *revoltgo.User {
	if user := b.session.State.User(uID); user != nil {
		return user
	}
	user, _ := b.session.User(uID)
	return user
}

// Get the correct display name and avatar for a given user in a given channel.
func (b *Bstoat) fetchNameAndAvatar(cID, uID string) (name, avatar string) {
	name = ""
	avatar = ""
	// Check the user's per-server profile
	if channel := b.getChannel(cID); channel != nil && channel.Server != nil {
		if member := b.getMember(uID, *channel.Server); member != nil {
			if member.Nickname != nil {
				name = *member.Nickname
			}
			if member.Avatar != nil {
				avatar = member.Avatar.URL("")
			}
		}
	}
	// Fallback to the global profile
	if name == "" || avatar == "" {
		if user := b.getUser(uID); user != nil {
			if name == "" {
				if user.DisplayName != nil {
					name = *user.DisplayName
				} else {
					name = user.Username
				}
			}
			if avatar == "" && user.Avatar != nil {
				avatar = user.AvatarURL("")
			}
		}
	}
	if name == "" {
		name = uID
	}
	return
}
