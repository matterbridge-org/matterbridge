# Stoat

- Status: Working
- Maintainers: @williamhorning
- Features: it'll do anything supported by the
  [lightning](https://codeberg.org/jersey/lightning) API plugin implementation
  of the matterbridge API

## Permissions

The Stoat bot will need the following permissions to ensure you don't run into
`MissingPermissions` errors: Manage Customization, Manage Role, Change
Nickname, Change Avatar, View Channel, Read Message History, Send Messages,
Manage Messages, Send Embeds, Upload Files, and Masquerade

To assign it permissions, you will need to create a new role, assign the role
these permissions, save the role, and then grant the bot the newly created role

## Configuration

**Basic configuration example:**

```toml
[stoat]
[stoat.chat]
Token="<your stoat token here>"

[[gateway]]
name="testing"
enable=true

[[gateway.inout]]
account="stoat.chat"
channel="channel ULID"
```
