# Slack

- Status: ???
- Maintainers: ???
- Features: ???

## Configuration

**Basic configuration example:**

```toml
[slack]
[slack.myslack]
RemoteNickFormat="{BRIDGE} - @{NICK}"
# this requires *two* different tokens
Token="xoxb-*****"
AppToken="xapp-*****"
# this will maps threads from other bridges on slack threads
PreserveThreading=true
```


## FAQ

### How to get create an account for my matterbridge bot?

See [account.md](account.md).

### Messages come from Slack API tester
Did you set `RemoteNickFormat`?    
Try adding `RemoteNickFormat="<{NICK}>"`

### Messages from other bots aren't getting relayed

If you're using `WebhookURL` in your Slack configuration, this is normal.
If you only have `Token` configuration, this could be a bug. Please open an issue.

### `"not_allowed_token_type"` error in the logs

If you get the message:

```
ERROR slack: Connection failed "not_allowed_token_type" &errors.errorString{s:"not_allowed_token_type"}
```

For more information look at:

- https://github.com/slackapi/node-slack-sdk/issues/921#issuecomment-570662540
- https://github.com/nlopes/slack/issues/654
- our issue https://github.com/42wim/matterbridge/issues/964

## Notes regarding bot based setup tokens

The bot based setup can currently operate in 2 modes:

- **socket mode Events API mode** (recommended, preferred)  
  this mode works with modern Slack apps and is recommended for setting up Slack bridges.
  However this mode requires **two** tokens to operate:
  - a _Bot User OAuth Token_ bot token having prefix `xoxb-`, configured with `Token=` option
  - an App token having prefix `xapp-`, configured with `AppToken=` option

- **legacy RTM mode**  
  previously the primary mode of operation for the bridge.
  This mode only works with bot token belonging to a *classic* Slack apps;
  and as of 2024-06-04 it's no longer possible to create any new classic Slack apps.
  This mode only requires a single bot token `Token=` to be configured.

  If you already have an existing bridge setups with classic Slack apps then it should
  continue to work as before without changing the existing Slack app settings or matterbridge config
  (leave the `AppToken=` unset or set to empty value).
  Should you want to set up a new bridge, then the socket mode Events API mode is the recommended (and the only) way forward.
