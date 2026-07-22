# IRC settings

> [!TIP]
> This page contains the details about IRC settings. More general information about IRC support in matterbridge can be found in [README.md](README.md).

## Charset

If you know your charset, you can specify it manually.  Set to "autodetect" to try to detect this automatically.
 
The selected charset will be converted to utf-8 when sent to other, non-irc bridges.

- Setting: **OPTIONAL**
- Default: "utf-8"
- Format: *string*
- Example:
  ```toml
  Charset="utf-8"
  ```

## ColorNicks

ColorNicks will show each nickname in a different color.
Only works in IRC right now.  Will be overridden by UseRelayMsg if both are set.

- Setting: **OPTIONAL**, **RELOADABLE**
- Format: *boolean*
- Example:
  ```toml
  ColorNicks=true
  ```

## DebugLevel

Debug log verbosity.

- Setting: **OPTIONAL**
- Default: *0*
- Format: *int*
- Example:
  ```toml
  DebugLevel=1
  ```

## JoinDelay

Delay in milliseconds between channel joins.
Only useful when you have a LOT of channels to join.

- Setting: **OPTIONAL**, **RELOADABLE**
- Default: *0*
- Format: *int*
- Example:
  ```toml
  JoinDelay=1000
  ```

## MessageDelay

Flood control.
Delay in milliseconds between each message send to the IRC server.

- Setting: **OPTIONAL**, **RELOADABLE**
- Default: *1300*
- Format: *int*
- Example:
  ```toml
  MessageDelay=1300
  ```

## MessageLength

Maximum length of message sent to irc server, including bot nick, user and hostname, channel name, formatted remote nick, etc.
If it exceeds, `<message clipped>` will be added to the message and multiple lines will be sent.
Can be overridden if the IRC server sets the LINELEN token in an ISUPPORT message.

- Setting: **OPTIONAL**, **RELOADABLE**
- Default: *512*
- Format: *int*
- Example:
  ```toml
  MessageLength=512
  ```

## MessageQueue

Maximum amount of messages to hold in queue. If queue is full 
messages will be dropped. 
`<message clipped>` will be add to the message that fills the queue.

- Setting: **OPTIONAL**, **RELOADABLE**
- Default: *30*
- Format: *int*
- Example:
  ```toml
  MessageQueue=30
  ```

## MessageSplit
Split messages on `MessageLength` instead of relying on the irc library to do it.

- Setting: **OPTIONAL**, **RELOADABLE**
- Default: *true*
- Format: *boolean*
- Example:
  ```toml
  MessageSplit=true
  ```

## Nick *
Your nick on irc. 

- Setting: REQUIRED
- Format: *string*
- Example:
  ```toml
  Nick="matterbot"
  ```

## NickServNick
If you registered your bot with a service like Nickserv on freenode.
Also being used when `UseSASL=true`
Note: when `UseSASL=true`, this is the name of *your* account.
Note: if you want do to quakenet auth, set NickServNick="Q@CServe.quakenet.org"

- Setting: **OPTIONAL**
- Format: *string*
- Example:
  ```toml
  NickServNick="nickserv"
  ```

## NickServPassword
The password you use if you registered your bot with a service like Nickserv on freenode.
Also being used when `UseSASL=true`
Also see `NickServNick`

- Setting: **OPTIONAL**
- Format: *string*
- Example:
  ```toml
  NickServPassword="secret"
  ```

## NickServUsername
Only used for quakenet auth.
See https://github.com/42wim/matterbridge/issues/263 for more info

- Setting: **OPTIONAL**
- Format: *string*
- Example:
  ```toml
  NickServUsername="username"
  ```

## NoSendJoinPart
Do not send joins/parts to other bridges
Currently works for messages from the following bridges: irc, mattermost, slack

- Setting: **OPTIONAL**
- Format: *boolean*
- Example:
  ```toml
  NoSendJoinPart=true
  ```

## Password 

Password for irc server (if necessary)

- Setting: **OPTIONAL**
- Format: *string*
- Example:
  ```toml
  Password="s3cret"
  ```

## Pingdelay

PingDelay specifies how long to wait to send a ping to the irc server.
You can use s for second, m for minute

- Setting: **OPTIONAL**, **RELOADABLE**
Default: "1m"
- Example:
  ```toml
  PingDelay="1m"
  ```

## RejoinDelay
Delay in seconds to rejoin a channel when kicked

- Setting: **OPTIONAL**, **RELOADABLE**
Default: 0
- Format: *int*
- Example:
  ```toml
  RejoinDelay=2
  ```

## RunCommands

RunCommands allows you to send RAW irc commands after connection

- Setting: **OPTIONAL**, **RELOADABLE**
- Format: *List<string>*
- Example:
  ```toml
  RunCommands=["PRIVMSG user :hello","PRIVMSG chanserv :something"]
  ```

## Server

irc server to connect to. 

- Setting: REQUIRED
- Format: *string* (hostname:port)
- Example:
  ```toml
  Server="irc.freenode.net:6667"
  ```

## StripMarkdown

Strips Markdown from messages

- Setting: **OPTIONAL**
- Format: *boolean*
- Example:
  ```toml
  StripMarkdown=true
  ```

## UseSASL

Enable SASL (PLAIN) authentication. (freenode requires this from eg AWS hosts)
It uses `NickServNick` and `NickServPassword` as login and password

- Setting: **OPTIONAL**
- Format: *boolean*
- Example:
  ```toml
  UseSASL=true
  ```

## UseTLS

Enable to use TLS connection to your irc server. 

- Setting: **OPTIONAL**
- Format: *boolean*
- Example:
  ```toml
  UseTLS=true
  ```

## UseRelayMsg

Enable to replace bot's nick with user's nick.
Will override Colornicks if both are set.
- `RemoteNickFormat` has to contain `/`.
The server has to support RELAYMSG.
Bot may need to be channel operator to use RELAYMSG.

- Setting: OPTIIONAL
- Format: *boolean*
- Example:
  ```toml
  UseRelayMsg=true
  ```

## UseRelayFallback

Enable to replace empty post-sanitizing relayed nick with a fallback.
This can potentially allow for anonymized messages to be sent to IRC bridges.
If this is set to false, and a sanitized nick results as empty,
then the message will be dropped instead of relayed.

- Setting: OPTIONAL
- Default: true
- Format: *boolean*
- Example:
  ```toml
  UseRelayMsg=true
  ```

## RelayFallbackNick

The nick to replace empty sanitized nicks when using UseRelayMsg

- Setting: OPTIONAL
- Default: "unknown"
- Format: *string*
- Example:
  ```toml
  RelayFallbackNick="unknown"
  ```

## VerboseJoinPart

Enable to show verbose users joins/parts (ident@host) from other bridges
Currently works for messages from the following bridges: irc

- Setting: **OPTIONAL **
- Format: *boolean*
- Example:
  ```toml
  VerboseJoinPart=true
  ```

## DoubleColonPrefix

> [!WARNING]
> We are unsure what this setting is doing. If noone provides
> actual documentation on the usecase, it will be removed
> in a future release.

Adds a leading colon (`:`) when a message starts with another colon
and contains no whitespace. For example, turns `:D` into `::D`, potentially
breaking emote/emoji shortcodes.

- Setting: **OPTIONAL**
- Format: *boolean*
- Example:
  ```toml
  DoubleColonPrefix=true
  ```
