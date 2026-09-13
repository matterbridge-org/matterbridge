// Copyright (c) 2026 William Horning
//
// Portions of this package originate from Lightning, wherein they are licensed
// under the MIT license and are available at codeberg.org/jersey/lightning.
// This file is under a dual-license to ensure it's compatible matterbridge's
// post-fork AGPLv3 License, and lightning's MIT license.
//
// SPDX-License-Identifier: AGPL-3.0-or-later OR MIT

package stoat

import (
	"strings"
	"time"

	"codeberg.org/jersey/lightning/pkg/lightning"
	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/matterbridge-org/matterbridge/bridge/helper"
)

func (b *Bstoat) startListeners() {
	go func() {
		for typingEvent := range b.plugin.ListenTyping() {
			b.Remote <- config.Message{
				Event:     config.EventUserTyping,
				UserID:    typingEvent.UserID,
				Channel:   typingEvent.ChannelID,
				Account:   b.Account,
				Protocol:  b.Protocol,
				Timestamp: time.Now(),
			}
		}
	}()

	go func() {
		for deleteEvent := range b.plugin.ListenDeletes() {
			b.Remote <- config.Message{
				Event:     config.EventMsgDelete,
				ID:        deleteEvent.EventID,
				Channel:   deleteEvent.ChannelID,
				Account:   b.Account,
				Protocol:  b.Protocol,
				Timestamp: time.Now(),
			}
		}
	}()

	go func() {
		for messageEvent := range b.plugin.ListenMessages() {
			for _, embed := range messageEvent.Embeds {
				messageEvent.Content += "\n\n" + embed.ToMarkdown()
			}

			for _, mention := range messageEvent.Mentions {
				switch mention.Type { //nolint:exhaustive
				case lightning.MentionEmoji:
					messageEvent.Content = strings.ReplaceAll(messageEvent.Content, mention.Mention,
						"["+mention.Name+"]("+mention.URL+")")
				case lightning.MentionTimestamp:
					messageEvent.Content = strings.ReplaceAll(messageEvent.Content, mention.Mention, mention.Name)
				default:
					messageEvent.Content = strings.ReplaceAll(messageEvent.Content, mention.Mention, "@"+mention.Name)
				}
			}

			b.Remote <- config.Message{
				Text:      messageEvent.Content,
				Channel:   messageEvent.ChannelID,
				Username:  messageEvent.Author.Username,
				UserID:    messageEvent.Author.ID,
				Avatar:    messageEvent.Author.ProfilePicture,
				Account:   b.Account,
				Protocol:  b.Protocol,
				Timestamp: messageEvent.Time,
				ID:        messageEvent.EventID,
				Extra:     map[string][]any{"file": attachmentToFileInfo(messageEvent.Attachments)},
			}
		}
	}()
}

func attachmentToFileInfo(files []lightning.Attachment) []any {
	attachments := make([]any, 0, len(files))

	for _, file := range files {
		fileBytes, err := helper.DownloadFile(file.URL)
		if err != nil {
			continue
		}

		attachments = append(attachments, config.FileInfo{
			Name: file.Name, URL: file.URL, Size: file.Size, Data: fileBytes, Comment: file.Description,
		})
	}

	return attachments
}
