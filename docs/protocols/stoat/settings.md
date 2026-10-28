# Stoat settings

For general information about Stoat support in matterbridge, see the
[README.md](README.md) file in this directory.

## token

Token to use when interacting with the Stoat API and Gateway.

- Setting: **REQUIRED**
- Format: *string*
- Example:
  ```toml
  token="YOUR_TOKEN_HERE"
  ```

## api_url

Optional, the URL to use when interacting with the Stoat API. Must be paired
with a `cdn_url`, `events_url`, and `frontend_url`.

- Setting: **OPTIONAL**
- Format: *URL string*
- Example:
  ```toml
  api_url="https://api.stoat.chat/0.8" # this is the default
  ```

## cdn_url

Optional, the URL to use when interacting with the Stoat CDN. Must be paired
with an `api_url`, `events_url`, and `frontend_url`.

- Setting: **OPTIONAL**
- Format: *URL string*
- Example:
  ```toml
  cdn_url="https://cdn.stoatusercontent.com" # this is the default
  ```

## events_url

Optional, the URL to use when interacting with the Stoat Gateway. Must be
paired with an `api_url`, `cdn_url`, and `frontend_url`.

- Setting: **OPTIONAL**
- Format: *URL string*
- Example:
  ```toml
  events_url="wss://events.stoat.chat" # this is the default
  ```

## frontend_url

Optional, the URL to use for links to the Stoat frontend. Must be paired with
an `api_url`, `cdn_url`, and `events_url`.

- Setting: **OPTIONAL**
- Format: *URL string*
- Example:
  ```toml
  frontend_url="https://stoat.chat" # this is the default
  ```
