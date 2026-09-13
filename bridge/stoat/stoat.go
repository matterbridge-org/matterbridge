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
	"encoding/base64"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path"

	"codeberg.org/jersey/lightning/pkg/lightning"
	"codeberg.org/jersey/lightning/pkg/platforms/stoat"
	"github.com/matterbridge-org/matterbridge/bridge"
	"github.com/matterbridge-org/matterbridge/bridge/config"
)

type Bstoat struct {
	*bridge.Config

	plugin lightning.Plugin
}

func New(cfg *bridge.Config) bridge.Bridger {
	return &Bstoat{
		Config: cfg,
	}
}

func (b *Bstoat) Send(msg config.Message) (string, error) {
	switch msg.Event {
	case config.EventMsgDelete:
		return msg.ID, b.plugin.DeleteMessage(msg.Channel, []string{msg.ID}, &lightning.MessageOptions{})
	case config.EventUserTyping:
		return "", b.plugin.SendTyping(msg.Channel, msg.UserID, &lightning.MessageOptions{})
	case "":
		attachments := make([]lightning.Attachment, 0, len(*msg.GetFileInfos(b.Log)))

		for _, file := range *msg.GetFileInfos(b.Log) {
			if file.URL != "" {
				uri, _ := url.Parse(file.URL)
				content := mime.TypeByExtension(path.Ext(uri.Path))

				attachments = append(attachments, lightning.Attachment{
					URL: file.URL, Name: file.Name, Size: file.Size, Type: content, Description: file.Comment,
				})
			} else if file.Data != nil {
				b64 := base64.StdEncoding.EncodeToString(*file.Data)
				content := http.DetectContentType(*file.Data)

				attachments = append(attachments, lightning.Attachment{
					URL: "data:" + content + ";base64," + b64, Name: file.Name, Size: file.Size,
					Type: content, Description: file.Comment,
				})
			}
		}

		res, err := b.plugin.SendMessage(&lightning.Message{
			Attachments: attachments, Author: lightning.MessageAuthor{
				ID: msg.UserID, Username: msg.Username, ProfilePicture: msg.Avatar, Color: "#FFFFFF",
			}, BaseMessage: lightning.BaseMessage{Time: msg.Timestamp, EventID: msg.ID, ChannelID: msg.Channel}, Content: msg.Text,
		}, &lightning.MessageOptions{})
		if err != nil {
			return "", err
		}

		return res[0], nil
	default:
		return "", nil
	}
}

func (b *Bstoat) Connect() error {
	var err error

	b.plugin, err = stoat.New(map[string]string{"token": b.GetString("token")}, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to Stoat: %w", err)
	}

	b.startListeners()

	return nil
}

func (b *Bstoat) JoinChannel(channel config.ChannelInfo) error {
	return nil
}
