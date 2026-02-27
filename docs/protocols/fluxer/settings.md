# Fluxer settings

> [!TIP]
> This page contains the details about Fluxer settings. More general information about Fluxer support in matterbridge can be found in [README.md](README.md).

## Server

Uid of your guild. You can get this by right click on your guild and click "Copy Community ID"

- Setting: **REQUIRED**
- Format: *string*
- Example:
  ```toml
  Server="yourguilduid"
  ```

## Token

Token to connect with Fluxer API/Gateway.

- Setting: **REQUIRED**
- Format: *string*
- Example:
  ```toml
  Token="YOUR_TOKEN_HERE"
  ```

## AllowMention

AllowMention controls which mentions are allowed.

If not specified, all mentions are allowed. Note that even when a mention is
not allowed, it will still be displayed nicely and be clickable. It just
prevents the ping/notification.

- Setting: **OPTIONAL**
- Format: *List[string]*
- Possible values:
  - `"everyone"` allows `@everyone` and `@here` mentions
  - `"roles"` allows `@role` mentions
  - `"users"` allows `@user` mentions
- Example:
  ```toml
  AllowMention=["everyone", "roles", "users"]
  ```

## ShowEmbeds

Shows title, description and URL of embedded messages (sent by other bots)

- Setting: **OPTIONAL**, **RELOADABLE**
- Format: *boolean*
- Example:
  ```toml
  ShowEmbeds=true
  ```

## UseUserName

Shows the username instead of the server nickname

- Setting: **OPTIONAL**, **RELOADABLE**
- Format: *boolean*
- Example:
  ```toml
  UseUserName=true
  ```

## UseDiscriminator

Show `#xxxx` discriminator with `UseUserName`

- Setting: **OPTIONAL**, **RELOADABLE**
- Format: *boolean*
- Example:
  ```toml
  UseDiscriminator=true
  ```
