# Matrix

- Status: ???
- Maintainers: @poVoq
- Features: ???

> [!WARNING]
> **Create a dedicated user first. It will not relay messages from yourself if you use your account**

## Configuration

> [!TIP]
> For detailed information about matrix settings, see [settings.md](settings.md)

**Basic configuration example:**

```toml
[matrix.mymatrix]
RemoteNickFormat="[{PROTOCOL}] <{NICK}> "
Server="https://matrix.org"
Login="yourlogin"
Password="yourpass"
# Alternatively, you can use MXID + a session Token + a device id
#MxID="@yourbot:example.net"
#Token="tokenforthebotuser"
#DeviceID="deviceidofmxidandtokenlogin"
```

## FAQ

### How to encrypt matterbridge messages to Matrix?

[matrix.test]
RemoteNickFormat="{NICK} ({LABEL}) "
Server="<https://domain.tld>"
Login="yourlogin"
Password="yourpass"
SessionFile="matrix_crypto.db" # sqlite database file used to store the login session persistently
PickleKey="yourreallylongandcomplicatedpickle" # a long password to use when accessing the session store
RecoveryKey="this thing isss real long bubb" # your account recovery key from matrix for this account
MxID="@yourusername:domain.tld" # your mxid from the logs
Token="your token from log output" # your token from the logs
DeviceID="yourdeviceid" # your deviceid from the logs

#### Steps for getting an encrypted connection working

MxID, Token and DeviceID are required for encryption even though they are optional normally

1. Generate a recovery key using your preferred Matrix client for your account and make sure to store it for later
2. Fill in the following

   - RemoteNickFormat (Optional)
   - Server
   - Login
   - Password
   - SessionFile (make sure this is readable and writable by the bot's run account)
   - PickleKey
   - RecoveryKey

3. Start your bot and log in making sure to keep a log or watch output
4. Copy these from the logs in to your configuration file

   - MxID
   - Token
   - DeviceID

5. Restart the bot and enjoy encrypted messaging

##### Notes

- If you enable encryption on a channel after enabling encryption on the bot, you may need to regenerate your RecoveryKey, Token and DeviceID before the bot can send encrypted messages
- If Matterbridge was present before encryption was enabled on a channel, it will not initialize crypto correctly and will continue sending unencrypted messages.
  1. Kick the bot from the room or leave the room with the bot's account (e.g in Element or some other client).
  2. Re-invite the bot to the room.
  3. The bot will get the encryption flag and start sending encrypted messages.
