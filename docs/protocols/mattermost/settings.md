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

Since Mattermost 12, the remote nick and avatar (`override_username` /
`override_icon_url`) are only displayed for posts made through an incoming
webhook, so this is the only way to show bridged messages under the name of
their author rather than the bot.

When `Token` (or `Login`/`Password`) is configured as well, the API is used
next to the webhook, so that bridged messages can still be threaded (with
`PreserveThreading`), edited and deleted. Edits and deletes require the
webhook to be **created by the same user as the `Token`**, because Mattermost
only lets the owner of a webhook (or users who may edit others' posts) modify
its posts. matterbridge logs a warning on startup when this is not the case.

With only a webhook (no `Token`/`Login`), messages can't be threaded, edited
or deleted.

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

## PreserveThreading

Map threads from other bridges on Mattermost threads (and the other way around
when the other bridge enables it too). This only works if the parent message
is still in the cache, which is flushed between restarts: replies to unknown
messages are sent with a `[thread]: ` prefix.

- Setting: **OPTIONAL**, **RELOADABLE**
- Format: *boolean*
- Example:
  ```toml
  PreserveThreading=true
  ```

## PreserveReactions

Relay reactions (emojis) added to and removed from messages, from and to
other bridges supporting them (currently Mattermost and Slack). This requires `Token` or `Login` (the API), also when using `WebhookURL`. Enable it on
each bridge that should receive reactions.

Reactions are added by the bridge user, once per emoji even if several
remote users reacted with it, and removed when the last of them removes it.
Like threads, this only works for messages still in the cache, which is
flushed between restarts. Emojis that don't exist on the destination (e.g.
custom emojis) are skipped; skin tones fall back to the default emoji when
needed.

- Setting: **OPTIONAL**, **RELOADABLE**
- Format: *boolean*
- Example:
  ```toml
  PreserveReactions=true
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
