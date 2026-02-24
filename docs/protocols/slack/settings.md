# Slack settings

> [!TIP]
> This page contains the details about slack settings. More general information about slack support in matterbridge can be found in [README.md](README.md).

## Debug

Extra slack specific debug info, warning this generates a lot of output.

- Setting: **OPTIONAL**, **RELOADABLE**
- Format: *boolean*
- Example:
  ```toml
  Debug=true
  ```

## PreserveThreading

Opportunistically preserve threaded replies between Slack channels.
This only works if the parent message is still in the cache.
Cache is flushed between restarts.
Note: Not currently working on gateways with mixed bridges of
both slack and slack-legacy type. Context in issue #624.

- Setting: **OPTIONAL**, **RELOADABLE**
- Format: *boolean*
- Example:
  ```toml
  PreserveThreading=true
  ```

## ShowUserTyping

Enable showing "user_typing" events from across gateway when available.
Protip: Set your bot/user's "Full Name" to be "Someone (over chat bridge)",
and so the message will say "Someone (over chat bridge) is typing".

- Setting: **OPTIONAL**, **RELOADABLE**
- Format: *boolean*
- Example:
  ```toml
  ShowUserTyping=true
  ```

## SyncTopic

Enable to sync topic/purpose changes from other bridges
Only works syncing topic changes from slack bridge for now

- Setting: **OPTIONAL**, **RELOADABLE**
- Format: *boolean*
- Example:
  ```toml
  SyncTopic=true
  ```

## Token

Token to connect with the Slack API to perform actions via its Web API.

- Setting: **REQUIRED** for bot-based setup, for both classic and modern Slack app.
- Format: *string*
- Example:
  ```toml
  Token="xoxb-*****"
  ```

## AppToken

App Token to connect with the Slack API using socket mode Events API to receive messages to bridge.

For a bot token belonging to a _classic_ Slack apps, this **MUST** be unset or set to empty value.
For modern socket mode Events API based setup, this **MUST** be set to the app token
with the `connections:write` scope associated to it.

- Setting: **REQUIRED** (see above remarks)
- Format: *string*
- Example:
  ```toml
  AppToken="xapp-*****"
  ```

### IconURL

> [!WARNING]
> NOT RECOMMENDED TO USE INCOMING/OUTGOING WEBHOOK.
> USE DEDICATED BOT USER WHEN POSSIBLE!

Icon that will be showed in slack.
The string "{NICK}" (case sensitive) will be replaced by the actual nick / username.
The string "{BRIDGE}" (case sensitive) will be replaced by the sending bridge.
The string "{LABEL}" (case sensitive) will be replaced by label= field of the sending bridge.
The string "{PROTOCOL}" (case sensitive) will be replaced by the protocol used by the bridge.

- Setting: **OPTIONAL**
- Format: *string*
- Example:
  ```toml
  IconURL="https://robohash.org/{NICK}.png?size=48x48"
  ```

### WebhookBindAddress

> [!WARNING]
> NOT RECOMMENDED TO USE INCOMING/OUTGOING WEBHOOK.
> USE DEDICATED BOT USER WHEN POSSIBLE!

Address to listen on for outgoing webhook requests from slack.
See account settings - integrations - outgoing webhooks on slack

- Setting: **OPTIONAL**
- Format: *string*
- Example:
  ```toml
  WebhookBindAddress="0.0.0.0:9999"
  ```

### WebhookURL

> [!WARNING]
> NOT RECOMMENDED TO USE INCOMING/OUTGOING WEBHOOK.
> USE DEDICATED BOT USER WHEN POSSIBLE!

Url is your incoming webhook url as specified in slack.
See account settings - integrations - incoming webhooks on slack

- Setting: **OPTIONAL**
- Format: *string*
- Example:
  ```toml
  WebhookURL="https://hooks.slack.com/services/yourhook"
  ```
