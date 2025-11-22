# Mastodon

- Status: Working
- Maintainers: @lil5
- Features: home, local, remote, direct toots

## Configuration

> [!TIP]
> For help getting a client id/secret/access token, see [application.md](application.md)

**Basic configuration example:**

```toml
[mastodon]
[mastodon.mymastodon]
Server="https://mastodon.social"
ClientID="xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
ClientSecret="xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
AccessToken="xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
```

## FAQ

### How to connect to a list?

Currently the only supported lists are: home, local, remote

```toml
[[gateway.inout]]
account="mastodon.mymastodon"
channel="home"
```

### How to connect to a direct message?

```toml
[[gateway.inout]]
account="mastodon.mymastodon"
channel="@name@mastodon.social"
```