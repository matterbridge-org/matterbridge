# XMPP settings

> [!TIP]
> This page contains the details about xmpp settings. More general information about xmpp support in matterbridge can be found in [README.md](README.md).

> [!NOTE]
> XMPP (the protocol) is also known as Jabber (the open federation). These
> two terms are used interchangeably. To learn more about Jabber/XMPP,
> see [joinjabber.org](https://joinjabber.org/).

## Jid

Jabber Identifier, the XMPP login for matterbridge's account.

- Setting: **REQUIRED**
- Format: *string*
- Example:
  ```toml
  Jid="user@example.com"
  ```

## MUC

The Multi User Chat (MUC) server where the bot will find the defined gateway
channels. At the moment, bridging a room on a different MUC requires creating
a separate account entry in the configuration.

TODO: test if a matterbridge instance can be connected to the same account
      with two configurations at the same time; this is allowed by XMPP
      protocol but requires matterbridge to behave properly in terms
      of XMPP protocol

- Setting: **REQUIRED**
- Format: *string*
- Example:
  ```toml
  Muc="conference.jabber.example.com"
  ```

## Nick 

Your nick in the rooms

- Setting: **REQUIRED**
- Format: *string*
- Example:
  ```toml
  Nick="xmppbot"
  ```

## NoTLS (DEPRECATED)

> [!WARNING]
> This setting has been deprecated. matterbridge will refuse to start if you are using it.
> You should use the new `UseDirectTls` and `NoStartTls` settings instead.

- Setting: **OPTIONAL**
- Format: *boolean*
- Example:
  ```toml
  NoTLS=true
  ```

## UseDirectTLS

Enables direct TLS connection to your server. Most servers by default only support StartTLS,
so this option should only be enabled if you know what you are doing. When `UseDirectTLS` is
not set, and `NoStartTls` is enabled, a plaintext connection is established, which
should only be used in a local testing environment.

- Setting: **OPTIONAL**
- Format: *boolean*
- Example:
  ```toml
  UseDirectTLS=true
  ```

## NoStartTLS

Disable StartTLS connection to your server. If you'd like to use direct TLS, enable
the `UseDirectTLS` setting. Otherwise, a plaintext connection is established, which
should only be used in a local testing environment.

- Setting: **OPTIONAL**
- Format: *boolean*
- Example:
  ```toml
  NoStartTLS=true
  ```

## Password

Password for the Jid's account.

- Setting: **REQUIRED**
- Format: *string*
- Example:
  ```toml
  Password="yourpass"
  ```

## Server

XMPP server to connect to.

- Setting: **REQUIRED**
- Format: *string* (hostname:port)
- Example:
  ```toml
  Server="jabber.example.com:5222"
  ```

## Mechanism

Force an explicit SASL mechanism for authentication. This is a very advanced setting
when debugging authentication problems and potential upstream go-xmpp authentication
bugs. If you don't understand it, you don't need it.

- Setting: **OPTIONAL**
- Format: *string*
- Example:
  ```toml
  Mechanism="PLAIN"
  ```
