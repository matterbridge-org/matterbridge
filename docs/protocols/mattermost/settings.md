# Mattermost settings

> [!TIP]
> This page contains the details about mattermost settings. More general information about mattermost support in matterbridge can be found in [README.md](README.md).

## Login

login of your bot.
Use a dedicated user for this and not your own!

- Setting: **REQUIRED** (when not using webhooks)
- Format: *string*
- Example:
  ```toml
  Login="yourlogin"
  ```

## NoTLS

Enable this to make a http connection (instead of https) to your mattermost.

- Setting: **OPTIONAL**
- Format: *boolean*
- Example:
  ```toml
  NoTLS=true
  ```

## Password

login of your bot.
Use a dedicated user for this and not your own!

- Setting: **REQUIRED** (when not using webhooks)
- Format: *string*
- Example:
  ```toml
  Password="yourpass"
  ```

## Server

The mattermost hostname. (do not prefix it with http or https)

- Setting: **REQUIRED**
- Format: *string*
- Example:
  ```toml
  Server="yourmattermostserver.domain"
  ```

## SkipVersionCheck

Skip the Mattermost server version checks that are normally done when connecting.
The usage scenario for this feature would be when the Mattermost instance is hosted behind a 
reverse proxy that suppresses "non-standard" response headers in flight.

- Setting: **OPTIONAL**
- Format: *boolean*
- Example:
  ```toml
  SkipVersionCheck=true
  ```

## Team

Your team on mattermost.

- Setting: **REQUIRED** (when not using webhooks)
- Format: *string*
- Example:
  ```toml
  Team="yourteam"
  ```

## Token

personal access token of the bot.
new feature since mattermost 4.1. See https://docs.mattermost.com/developer/personal-access-tokens.html
you can use token instead of login/password.

- Setting: **OPTIONAL** (when not using webhooks)
- Format: *string*
- Example:
  ```toml
  Token="abcdefghijklm"
  ```

### WebhookURL

> [!WARNING]
> NOT RECOMMENDED TO USE INCOMING/OUTGOING WEBHOOK.
> USE DEDICATED BOT USER WHEN POSSIBLE!

Url is your incoming webhook url as specified in mattermost.
See account settings - integrations - incoming webhooks on mattermost.
If specified, messages will be sent to mattermost using this URL

- Setting: **OPTIONAL**
- Format: *string*
- Example: 
  ```toml
  WebhookURL="https://yourdomain/hooks/yourhookkey"
  ```

### WebhookBindAddress

> [!WARNING]
> NOT RECOMMENDED TO USE INCOMING/OUTGOING WEBHOOK.
> USE DEDICATED BOT USER WHEN POSSIBLE!

Address to listen on for outgoing webhook requests from mattermost.
See account settings - integrations - outgoing webhooks on mattermost.
If specified, messages will be received from mattermost on this ip:port
(this will only work if `WebhookURL` is also configured)

- Setting: **OPTIONAL**
- Format: *string*
- Example:
  ```toml
  WebhookBindAddress="0.0.0.0:9999"
  ```

### IconURL

> [!WARNING]
> NOT RECOMMENDED TO USE INCOMING/OUTGOING WEBHOOK.
> USE DEDICATED BOT USER WHEN POSSIBLE!

Icon that will be showed in mattermost.
This only works when `WebhookURL` is configured.

- Setting: **OPTIONAL**
- Format: *string*
- Example:
  ```toml
  IconURL="http://youricon.png"
  ```

## SkipTLSVerify

Enable to not verify the certificate on your mattermost server, e.g. when using selfsigned certificates.

- Setting: **OPTIONAL**
- Format: *boolean*
- Example:
  ```toml
  SkipTLSVerify=false
  ```

## UseUserName

UseUserName shows the username instead of the server nickname.

- Setting: **OPTIONAL**, **RELOADABLE**
- Format: *boolean*
- Example:
  ```toml
  UseUserName=false
  ```

## NickFormatter

How to format the list of IRC nicks when displayed in mattermost.

- Setting: **OPTIONAL**, **RELOADABLE**
- Format: *string* ("plain" or "table")
- Example:
  ```toml
  NickFormatter=plain
  ```

## NicksPerRow

How many nicks to list per row for formatters that support this.

- Setting: **OPTIONAL**, **RELOADABLE**
- Format: *int*
- Example:
  ```toml
  NicksPerRow=4
  ```

## PrefixMessagesWithNick

Whether to prefix messages from other bridges to mattermost with the sender's nick. Useful if username overrides for incoming webhooks isn't enabled on the mattermost server. If you set PrefixMessagesWithNick to true, each message from bridge to Mattermost will by default be prefixed by "bridge-" + nick. You can, however, modify how the messages appear, by setting (and modifying) RemoteNickFormat.

- Setting: **OPTIONAL**, **RELOADABLE**
- Format: *boolean*
- Example:
  ```toml
  PrefixMessagesWithNick=false
  ```

## EditDisable

Disable sending of edits to other bridges.

- Setting: **OPTIONAL**, **RELOADABLE**
- Format: *boolean*
- Example:
  ```toml
  EditDisable=false
  ```

## EditSuffix

Message to be appended to every edited message.

- Setting: **OPTIONAL**, **RELOADABLE**
- Format: *string*
- Example:
  ```toml
  EditSuffix=" (edited)"
  ```
