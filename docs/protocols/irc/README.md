# IRC

- Status: Working
- Maintainers: @poVoq, @selfhoster1312
- Features: ???

## Configuration

> [!TIP]
> For detailed information about irc settings, see [settings.md](settings.md)

**Basic configuration example:**

```toml
[irc.myirc]
RemoteNickFormat="[{PROTOCOL}] <{NICK}> "
Server="irc.libera.chat:6667"
Nick="yourbotname"
Password="yourpassword"
# Enable SASL on modern servers like irc.libera.chat
# UseSASL=true
```

## FAQ

### How to connect to a password-protected channel?

```toml
[[gateway.inout]]
account="irc.myirc"
channel="#some-passworded-channel"
options = { key="password" }
```

### How to connect to OFTC-style NickServ

```toml
[irc.myirc]
Nick="yournick"
Server="irc.oftc.net:6697"
RunCommands=["PRIVMSG nickserv :IDENTIFY yourpass yournick"]
```

# FAQ

## Why can't matterbridge share files on IRC without a mediaserver?

If you see in the chat the error « Could not share file FILE (no mediaserver configured) »,
it means matterbridge tried to share a file which has no public URL when no media server
is configured. Files from networks which provide us with a public URL are unaffected
and can be shared to IRC freely.

Some files such as Matrix file attachments are shared privately and while matterbridge
can access the file content (raw bytes), there's no link to the file. In order to produce
a link that can be shared on IRC, you need to enable [a mediaserver](../../advanced/mediaserver.md),
which requires a public-facing web server.
