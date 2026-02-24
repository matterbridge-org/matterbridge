# A Matterbridge integration for your Slack Workspace

> [!IMPORTANT]
> the bot based setup is the only supported method of integration,
> any other methods that was previously listed here is deprecated and no longer supported,
> may just straight up not working, and will be removed in the future.

## Bot-based Setup

Slack's model for bot users and other third-party integrations revolves around Slack Apps. They have been around for a while and are the only and default way of integrating services like Matterbridge going forward.

Slack Docs: [Bot user tokens](https://api.slack.com/docs/token-types#bot)

You will be creating **two** tokens, both are required to configure a Slack bridge:

- a _Bot User OAuth Token_ Bot token, with prefix `xoxb-`
- an App Token, with prefix `xapp-`

### Create the Slack App

1. Navigate to Slack's ["Your Apps" page](https://api.slack.com/apps) and log into an account that has administrative permissions over the Slack Workspace (server) that you want to sync with Matterbridge.

   <img alt="Create New App" width="1059" height="315" alt="image" src="https://github.com/user-attachments/assets/37f6a034-2734-4f40-a678-4204782e7c56" />

2. Click the "Create New App" button, select "From scratch", Choose any name for your app and select the desired workspace, and then submit.

   <img alt="Create New App dialogs" width="1116" height="551" alt="image" src="https://github.com/user-attachments/assets/a94ee7ee-1cb4-4fb3-99bb-46a628a2cea8" />

### Enable socket mode and Creating App Token

The bridge connects to Slack using socket mode Events API, but they have to be enabled first.

Navigate to the "Socket Mode" page via the menu on the left. Toggle on "Connect using Socket Mode" and and follow the dialog to generate a new App Token with `connections:write`, then submit.

<img alt="Toggle for enable Socket mode" width="706" height="252" alt="image" src="https://github.com/user-attachments/assets/d0408fe6-aef9-4144-a177-b735e292c836" />

Note down the App Token (with prefix `xapp-`) which you will use in Matterbridge configuration.

### Grant bot token scopes and install the Slack App

You'll need to give your new Slack App, and thus the bot, the right permissions on your Slack Workspace.

2. Click "OAuth & Permissions" in the menu on the left, and scroll down to the "Scopes" section. click "Add an OAuth Scope" under "Bot Token Scopes" section.

   <img alt="Add OAuth Scopes for Bot Token" width="587" height="372" alt="image" src="https://github.com/user-attachments/assets/b4aa1bd4-df56-4e35-8654-4b8d518d5324" />

3. Add the following scopes:
   - `calls:read`
   - `channels:history`
   - `channels:read`
   - `chat:write.customize`
   - `chat:write`
   - `dnd:read`
   - `files:read`
   - `files:write`
   - `groups:history`
   - `groups:read`
   - `im:history`
   - `im:write`
   - `mpim:history`
   - `mpim:read`
   - `mpim:write`
   - `pins:write`
   - `reactions:read`
   - `reactions:write`
   - `remote_files:read`
   - `remote_files:share`
   - `remote_files:write`
   - `team:read`
   - `users.profile:read`
   - `users:read.email`
   - `users:read`
   - `users:write`

   <img alt="Bot Token scopes" width="589" height="606" alt="image" src="https://github.com/user-attachments/assets/c51062d1-ea01-4770-bdf1-d78c4ae3ee3e" />

4. Scroll to the top of the same "OAuth & Permissions" page and click on the "Install to Workspace" (or "Reinstall to Workspace") button:

   <img alt="(re)Install the App" width="971" height="745" alt="image" src="https://github.com/user-attachments/assets/6fd5ea2f-f608-4e4f-a02b-a2fbd0d9fb98" />

   Confirm that the authorizations you just added are OK:

   <img alt="App install authorization" width="879" height="733" alt="image" src="https://github.com/user-attachments/assets/4ee40be3-2c32-401c-93cd-d7aeb1759f09" />

5. Once the App has been installed, the top of the "OAuth & Permissions" page will show a "Bot User OAuth Token" Bot token with prefix `xoxb-`. You will use this in your Matterbridge configuration together with the App Token above:

   <img alt="Bot User OAuth Token" width="692" height="343" alt="image" src="https://github.com/user-attachments/assets/5089acb5-6b1b-4a4e-b3ae-87d8bffecf2e" />

### Invite the bot to channels synced with Matterbridge

The only thing that remains now is to set up the newly created bot on the Slack Workspace itself.

1. On your Slack server you need to add the newly created bot to the relevant channels. Simply use the `/invite @<botname>` command in the chatbox.

   <img width="527" alt="Invite the bot user to a channel" src="https://user-images.githubusercontent.com/22248748/119407127-c9742f00-bcb1-11eb-870d-7e38335f58df.png">

2. Repeat the invite process for each channel that Matterbridge needs to sync.
   :warning: Also, don't forget to do this in the future when you want to sync more channels.

Now you are all set to go. Just configure and start your Matterbridge instance and see the messages flowing.

   <img width="487" alt="Hello from Zulip!" src="https://user-images.githubusercontent.com/22248748/119406733-320edc00-bcb1-11eb-9e20-28ecd0b98d5d.png">
