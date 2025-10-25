admins = { }

-- Github action has 5222 reserved?
c2s_ports = { 52222 }

modules_enabled = {
		"disco"; -- Service discovery
		"roster"; -- Allow users to have a roster. Recommended ;)
		"saslauth"; -- Authentication for clients and servers. Recommended if you want to log in.
		"tls"; -- Add support for secure TLS on c2s/s2s connections
		"blocklist"; -- Allow users to block communications with other users
		"bookmarks"; -- Synchronise the list of open rooms between clients
		"carbons"; -- Keep multiple online clients in sync
		"dialback"; -- Support for verifying remote servers using DNS
		"limits"; -- Enable bandwidth limiting for XMPP connections
		"pep"; -- Allow users to store public and private data in their account
		"private"; -- Legacy account storage mechanism (XEP-0049)
		"smacks"; -- Stream management and resumption (XEP-0198)
		"vcard4"; -- User profiles (stored in PEP)
		"vcard_legacy"; -- Conversion between legacy vCard and PEP Avatar, vcard
		"account_activity"; -- Record time when an account was last used
		--"cloud_notify"; -- Push notifications for mobile devices
		"csi_simple"; -- Simple but effective traffic optimizations for mobile devices
		"invites"; -- Create and manage invites
		"invites_adhoc"; -- Allow admins/users to create invitations via their client
		"invites_register"; -- Allows invited users to create accounts
		"ping"; -- Replies to XMPP pings with pongs
		"register"; -- Allow users to register on this server using a client and change passwords
		"time"; -- Let others know the time here on this server
		"uptime"; -- Report how long server has been running
		"version"; -- Replies to server version requests
		"mam"; -- Store recent messages to allow multi-device synchronization
		--"turn_external"; -- Provide external STUN/TURN service for e.g. audio/video calls

	-- Admin interfaces
	-- "admin_adhoc"; -- Allows administration via an XMPP client that supports ad-hoc commands
	-- "admin_shell"; -- Allow secure administration via 'prosodyctl shell'

	-- HTTP modules
		--"bosh"; -- Enable BOSH clients, aka "Jabber over HTTP"
		--"http_openmetrics"; -- for exposing metrics to stats collectors
		--"websocket"; -- XMPP over WebSockets
}

c2s_require_encryption = false
allow_unencrypted_plain_auth = true

-- Don't use this in prod!
-- See https://prosody.im/doc/modules/mod_muc
muc_room_locking = false
muc_room_lock_timeout = 0

modules_disabled = {
	"s2s"; -- Handle server-to-server connections
}

limits = {
	c2s = {
		rate = "10kb/s";
	};
	s2sin = {
		rate = "30kb/s";
	};
}

-- Users are logged in with a random JID.
authentication = "any"
-- Database is kept in memory (this is only for tests)
storage = "memory"

archive_expires_after = "1w" -- Remove archived messages after 1 week

log = {
	debug = "*console";
}


VirtualHost "matterbridge-test.localhost"
Component "muc.matterbridge-test.localhost" "muc"
  modules_enabled = { "muc_mam" }

---Set up a file sharing component
--Component "share.example.com" "http_file_share"
