# Stoat

- Status: Working
- Maintainers: ???
- Features: send messages

## Configuration

> [!TIP]
> For detailed information about Stoat settings, see [settings.md](settings.md)

**Basic configuration example:**

```toml
[stoat]
[stoat.mystoat]
RemoteNickFormat="[{PROTOCOL}] {NICK}"
Token="Yourtokenhere"

[[gateway]]
name="testing"
enable=true

[[gateway.inout]]
account="stoat.mystoat"
channel="ID:01ARZ3NDEKTSV4RRFFQ69G5FAV"
```

## FAQ

##### Notes
