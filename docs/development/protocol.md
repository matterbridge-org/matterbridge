# Implementing a new protocol

This guide explains how to create a new protocol backend to support a new gateway/bridge in matterbridge.

## Step-by step list

- [ ] Create a new catalog in [`/bridge` folder](https://github.com/42wim/matterbridge/tree/master/bridge) and a main file named after the bridge you are creating, such as `whatsapp.go`
- [ ] Implement a [`Bridger` interface](https://github.com/42wim/matterbridge/blob/2cfd880cdb0df29771bf8f31df8d990ab897889d/bridge/bridge.go#L11-L16)
- [ ] Mention your bridge exists in [`/gateway/bridgemap/bridgemap.go`](https://github.com/42wim/matterbridge/blob/master/gateway/bridgemap/bridgemap.go)
- [ ] Divide functionality in several files, as it is done for [slack](https://github.com/42wim/matterbridge/tree/master/bridge)
  - `yourbridge.go` with main struct and implementation of the `Bridger` interface
  - `handlers.go` with handling messages incoming to Bridge
  - `helpers.go` for all the misc functions and helpers
- [ ] Minimal set of features is sending and receiving text messages working.
- [ ] Documentation
  - [ ] Add a [sample configuration](https://github.com/42wim/matterbridge/commit/6372d599b1ca2497aa49142d10496f345041b678#diff-0fcc5f77f08a4f4106d2da34c4dcd133) of your bridge to `matterbridge.toml.sample` and explain all the custom options
  - [ ] Add your bridge to README
  - [ ] Document all exported functions
- [ ] Run `golint` and `goimports` and clean the code
- [ ] Send a PR

## Features

Below is a feature list that you might copy to your issue.

Features:
- [ ] Connect to external service
- [ ] Get all active chats
- [ ] Check if chosen channels exist externally
- [ ] Connect to chosen channel
- [ ] Show nicknames in external service
- [ ] Show nicknames in relayed messages
- [ ] Test if multiple channels are working
- [ ] Show profile pictures from your bridge in relayed messages
- [ ] Show profile picture in your bridge
- [ ] Handle reply/thread messages
- [ ] Handle deletes
- [ ] Handle edits
- [ ] Handle notifications 
- [ ] Create a channel if it doesn't exist
- [ ] Sync channel metadata (name, topic, etc.)
- [ ] Document settings in `matterbridge.toml.sample`
- [ ] Document bridge in README
- [ ] Explain setting up the bridge process for users in the wiki
- [ ] Add screenshots from your bridge in the wiki
- [ ] Document code

Handle messages
- [ ] text from the bridge
- [ ] text to the bridge
- [ ] image
- [ ] audio
- [ ] video
- [ ] contacts?
- [ ] any other?


## FAQ

**How can I set the default RemoteNickFormat for a protocol so users don't have to do it in a config file?**

@42wim?

**Why on Slack I see bot name instead of remote username?**

Check if you:
- [ ] did set `Message.Username` on the message being relayed
- [ ] did set `RemoteNickFormat` in config file

**Sending message to the bridge don't work**

- [ ] Channels must match. While sending the message to the bridge make sure that you set the `config.Message.Channel` field to channel as it is mentioned in the config file.

### Handling HTTP requests

> [!TIP]
> If your protocol doesn't do HTTP requests at all, you do not have to read this section.

Every matterbridge bridge instance as defined in the config has its own dedicated HTTP client initiated when the program starts. It is used by
HTTP helpers (explained below) but may also be used directly as `b.HttpClient`.

#### Custom HTTP client

The HTTP client is initiated in the `NewHttpClient` method defined in [bridge/bridge.go](../../bridge/bridge.go), and can be overridden in your bridge class.

For example, if your protocol `foo` requires custom settings, such as going through tor, you would do something like:

```go
func (b *Bfoo) NewHttpClient(http_proxy string) (*http.Client, error) {
  // Create a new custom client here
}
```

> [!WARNING]
> Unless your customization requires to override the `http_proxy` passed as first argument to the constructor, don't forget to respect the defined proxy setting.

#### Custom HTTP requests

Every HTTP request emitted by your bridge is initiated in the `NewHttpRequest` method defined in [bridge/bridge.go](../../bridge/bridge.go), and can be overridden in your bridge class:

```go
func (b *Bfoo) NewHttpRequest(method, uri string, body io.Reader) (*http.Request, error) {
  // Create a new custom request here
}
```

This is useful for protocols which require setting custom HTTP headers, such as cookies or `Authorization` headers.

> [!INFO]
> This constructor is used by matterbridge's internal HTTP helpers, so by setting your custom headers in your bridge's
> `NewHttpRequest` method, they will be respected when using the helpers.

#### Downloading remote files

If your bridge needs to download files over HTTP, you can use matterbridge's internal helpers.
In the most common cases, you can use the two helpers `AddAvatarFromURL` (for user avatars) and `AddAttachmentFromURL`.

> [!WARNING]
> In all cases, it's very important to perform such HTTP operations in the background so you don't block
> matterbridge on a response that may succeed or timeout.
>
> ```go
> if hasAttachments(m) {
>   go func() {
>     err := handleAttachments(rmsg, m)
>     if err != nil {
>       b.Log.WithError(err).Errorf("Downloading attachment failed")
>       return
>     }
>     // Spreading the message (with the attachment) to other bridges takes
>     // place in the background goroutine
>     b.Remote <- rmsg
>   }()
>   // That entire message is being handled in the background, skip to the next message
>   continue
> }
> ```

TODO: what happens when the filename is not set? can we guess it from the URL/content-type? should we error if
      it's not explicit and cannot be inferred?
TODO: how is the ID used? is this param used at all or can we safely remove it?
TODO: should we take an optional hash to avoid useless requests for files we already have? since hash is already
      calculated later in the helpers

If you need to somehow inspect or treat the raw data bytes from a successful HTTP GET request before inserting it
as an attachment to the received message, you may use the `HttpGetBytes` method, which wll only succeed if the
returned HTTP status code is 200.

If you need more custom logic, such as a custom HTTP verb or headers specific to this request, you may use
the `HttpClient` field directly, along with the `NewHttpRequest` method:

```go
// If you willingly want to avoid `http_proxy` settings and/or your bridge's request constructor,
// use http.NewRequest here.
req, err := b.NewHttpRequest("GET", uri, "")
if err != nil {
  continue
}

// Customise the http request
...

// Send the request
resp, err := b.HttpClient.Do(req)
...
```

If your protocol can know in advance the size of the remote attachment, you can compare it with the maximum
download size to avoid too big requests altogether:

```go
for _, attach := range m.Attachments {
  if int64(attach.Size) > b.General.MediaDownloadSize {
    // Ignore this specific attachment (file too big)
    b.Log.Warnf("Attachment too big to download: %s has size %#v (MediaDownloadSize is %#v)", name, size, b.General.MediaDownloadSize)
    continue
  }
  ...
}
```

#### Uploading files to a remote server

If you need to upload files to a web server, you can use the `HttpUpload` helper method. It's similar to the `HttpGetBytes` method, but takes
two additional arguments:

- `headers` (`map[string][string]`), because you may need to set a specific `Content-Type` or `Authorization` header to perform the upload
- `ok_status` (`[]int`), because the remote server may have different success codes, eg. `200`/`201`, or even `302` for duplicate files

> [!WARNING]
> Just like with HTTP downloads, it's **very important** to perform upload operations in the background.

### Handling file attachments

Most protocols support sending files, such as images and other documents. How they are displayed, and how they are transferred changes
in every case, but there's two main approaches:

- in-band attachments (mumble): raw content bytes are sent within a protocol message
- out-of-band attachments (XMPP/matrix/etc): content is uploaded to a different server, and a URL is passed along in the protocol

For out-of-band attachments, see the HTTP upload/download section above.

#### In-band attachments

To receive raw bytes from in-band attachments, you can use the `AddAttachmentFromBytes` and `AddAvatarFromBytes` helper methods. They
both expect that you provide a filename in advance.

> [!NOTE]
> All protocols currently support importing data bytes into matterbridge, but not all of them support sending raw
> bytes to their own network. See issue [#50](https://github.com/matterbridge-org/matterbridge/issues/50) for a comparison table
> and broader discussion about these limitations.

TODO: what happens when no filename is set? Do we try to guess the mimetype to figure out an extension, and use the SHA hash as a basename?

To send in-band attachments, you can use the `FileInfo.Data` field which contains the raw attachment bytes.

#### Handling attachment errors

In the upstream past, matterbridge produced a message with `msg.Event = config.EventFileFailureSize`. However, this was not really documented,
especially how to handle mixed successful/errored attachments. It apparently was just discarded in `gateway/handlers.go` in the `handleMessage`
function and not handled gracefully by specific bridges.

At the moment, it is recommended to simply log errors from attachments, and proceed with further attachments ignoring failed ones.
