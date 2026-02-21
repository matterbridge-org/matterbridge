> [!NOTE]
> This is a community-maintained fork [42wim/matterbridge](https://github.com/42wim/matterbridge), because
> there is not a lot of activity upstream and lots of bugfixes were not merged.
>
> **Get involved now and [become a maintainer for your favorite network/protocol](https://github.com/matterbridge-org/community/issues/1)!**

<div align="center">

# matterbridge

![Matterbridge Logo](img/matterbridge-notext.gif)<br />
**A simple chat bridge**<br />
Letting people be where they want to be with the magic of [interoperability](https://en.wikipedia.org/wiki/Interoperability).<br />

---

[![Download stable](https://img.shields.io/github/release/42wim/matterbridge.svg?label=download%20stable)](https://github.com/42wim/matterbridge/releases/latest)

---

</div>

matterbridge is a solution to connect users on different platforms/protocols, allowing them to chat in the best conditions possible. Despite the name, Matter<em>most</em> isn't required to run matter<em>bridge</em>.

> [!WARNING]
> This fork has edited the history of the project to remove ~100MB of vendoring.
> This will faciliate review of new PRs, see [community/#5](https://github.com/matterbridge-org/community/issues/5)
> for the reasons why we did this and how to make sure we didn't introduce backdoors in this process.

## Features

Many features are available, but not all of them are supported on all protocols:

- [x] Bridge many rooms from supported protocols
- [x] Message threads/topics, and replies
- [x] Attachments, file uploads, and inline images
- [x] Transparent bridging with spoofed usernames and avatars
- [x] Private groups

**The complete and up-to-date list of supported protocols is in [docs/protocols/](docs/protocols/).** Additionally, we have an [API for 3rd party integration](https://github.com/42wim/matterbridge/wiki/Features#api) if you'd like to add a custom bridge without implementing it in this codebase.

![Screenshot of users discussing from different networks using matterbridge](https://user-images.githubusercontent.com/849975/52647227-9c3a5300-2ee4-11e9-9c57-ea096473aba8.png)

## Getting started with matterbridge

Get matterbridge up-and-running in a few minutes in 3 simple steps:

- [Setting up matterbridge](docs/setup.md) (see [docs/compiling.md](docs/compiling.md) for compiling from source)
- [Configuring matterbridge](docs/config.md)
- [Running matterbridge](docs/running.md) (CLI or as a systemd service)

## Documentation

See [docs/](docs/) folder in this repository.

## Contributing

You are welcome to submit pull requests, report bugs and request new features. matterbridge is a volunteer-run project and you are expected to behave with respect for the maintainers and other users. In particular, harassment and hate speech are not welcome.

For more development guidelines, see [docs/development/](docs/development/).

This project is licensed under the [GNU AGPLv3 license](LICENSE) since after commit `20988f6446c6ad3ea416044712e634d3ed85ee53`. It was relicensed following discussion in [commmunity/#10](https://github.com/matterbridge-org/community/issues/10). Apart from the obvious advantages of copyleft to promote innovation and cooperation, in very practical terms, we had to use either `GPL` or `AGPL` to include the `whatsappmulti` bridge in official builds and deprecate the broken legacy `whatsapp` bridge. When contributing to matterbridge development, you agree that your contributions will be published under that license.

Commits up-to `20988f6446c6ad3ea416044712e634d3ed85ee53` remain available under the looser [Apache License 2.0](LICENSE.old). 

### Chat with us

Questions or want to see the bridge in action? Join us on:

- federated networks: [Jabber/XMPP][mb-xmpp], [Matrix][mb-matrix]
- non-free centralized networks: [Discord][mb-discord]
- self-hostable centralized networks: [IRC][mb-irc], [Libera.chat][mb-legacy]

## Related projects

- [jwflory/ansible-role-matterbridge](https://galaxy.ansible.com/jwflory/matterbridge) (Ansible role to simplify deploying Matterbridge)
- [matterbridge autoconfig](https://github.com/patcon/matterbridge-autoconfig)
- [matterbridge config viewer](https://github.com/patcon/matterbridge-heroku-viewer)
- [matterbridge-heroku](https://github.com/cadecairos/matterbridge-heroku)
- [mattermost-plugin](https://github.com/matterbridge/mattermost-plugin) - Run matterbridge as a plugin in mattermost
- [isla](https://github.com/alphachung/isla) (Bot for Discord-Telegram groups used alongside matterbridge)

## Thanks

Matterbridge wouldn't exist without amazing libraries, without [@42wim](https://github.com/42wim) who started the project, and without the 100+ contributors who participated in this adventure. See [docs/credits.md](docs/credits.md) for more complete credits.

<!-- Links -->

[mb-discord]: https://discord.gg/c9Ht6UTnQU
[mb-irc]: ircs://irc.f-hub.org:6697/#matterbridge
[mb-legacy]: https://web.libera.chat/#matterbridge
[mb-matrix]: https://matrix.to/#/#matterbridge:matrix.f-hub.org
[mb-xmpp]: xmpp:matterbridge@chat.f-hub.org?join
