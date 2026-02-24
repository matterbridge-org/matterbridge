# Fluxer

- Status: Working
- Maintainers: @Danct12
- Features: send, delete, edit messages

## Configuration

> [!TIP]
> For detailed information about Fluxer settings, see [settings.md](settings.md)

**Basic configuration example:**

```toml
[fluxer]
[fluxer.myfluxer]
RemoteNickFormat="[{PROTOCOL}] <{NICK}> "
Token="######"
Server="uid of guild"
# Map threads from other bridges on fluxer replies
PreserveThreading=true

[[gateway]]
name="testing"
enable=true

[[gateway.inout]]
account="fluxer.myfluxer"
channel="uid of channel"
```

## FAQ

##### Notes
