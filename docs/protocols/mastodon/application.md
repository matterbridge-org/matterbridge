# Add application to mastodon

1. Create an mastodon Application

   Go to this url, change the domain to your mastodon website: https://mastodon.social/settings/applications/new
   
2. Set the following values:
   
   **Application name:** `MatterBridge`

   **Redirect URL:** `urn:ietf:wg:oauth:2.0:oob`

   **Scopes:**

   Check the following: `read`, `profile`, `write:conversations`, `write:statuses`

    Then, save changes.

3. Copy tokens to matterbridge.toml

   ```toml
   [mastodon]
   [mastodon.mymastodon]
   Server = "https://mastodon.social"
   ClientID = "<Client key>"
   ClientSecret = "<Client secret>"
   AccessToken = "<Your access token>"
   ```
