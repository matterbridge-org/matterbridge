Matterbridge is not going to implement it's own "mediaserver" instead we make use of other tools that are good at this sort of stuff. 
This mediaserver will be used to upload media to services that don't have support for uploading images/video/files.   
At this moment this is xmpp and irc

There are 2 options to set this up:
* You already have a webserver running
   * Matterbridge runs on the same server see [local download](#use-local-download)
   * Matterbridge runs on another server. If the webserver is using caddy, see [caddy](#use-remote-upload)
* You already have a S3-compatible storage, then you could use [S3](#s3-minio--remote-upload-using-s3-compatible-storage)
* You don't have a webserver running
   * See [caddy](#use-remote-upload)

# Use remote upload

# S3 (MinIO) — remote upload using S3-compatible storage

Matterbridge can upload media to S3-compatible object stores. This is useful when you want a hosted, scalable Mediaserver and you have (or run) an S3 endpoint such as MinIO.

Key points
* Use an s3:// bucket path for uploads; Matterbridge will put objects into that bucket and return URLs based on MediaServerDownload (or the S3 public endpoint). Additional part in s3:// are treated as a prefix to a file (same behaviour as in `awscli-v2`)
* For MinIO you normally enable path-style requests and provide the endpoint + credentials.

Sample matterbridge configuration (in [general])
```
[general]
# tell matterbridge to upload to the bucket
MediaServerUpload="s3://matterbridge"
# public URL base where objects will be served from (path style, this will be treated as a prefix to URL)
MediaServerDownload="https://minio.example.com/matterbridge"

# S3 / MinIO connection settings (common names used by many S3 clients)
S3Endpoint="https://minio.example.com:9000"
S3AccessKey="minioadmin"
S3SecretKey="minioadmin"
# MinIO typically requires path style for bucket paths
S3ForcePathStyle=true
# To use presigned URLs instead of public buckets. Presigned URL will be valid for 7 days.
# when using this setting MediaServerDownload is ignored.
# Please note that this produces awful, long links.
S3Presign=false
```

Notes and recommendations
* Public buckets are required for links to work, since users need to have permission to read files that were submitted by matterbridge.
* Adjust credentials and endpoints for your environment. For MinIO on a nonstandard port or local testing, use the correct host:port in S3Endpoint and in MediaServerDownload.

## Caddy
In this case we're using caddy for upload/downloading media. 
Caddy has automatic https support, so I'm going to describe this for https only.


### caddy install / configuration
Go to https://caddyserver.com/download   
Enable `http.upload` as plugin

Make sure the process you're running caddy with has read/write access to `/var/www/upload/`

Sample Caddyfile
```
yourserver.com:443 {
   log stdout
   root /var/www/upload/
   browse
   basicauth /web/upload a_user a_password
   upload /upload {
      to "/var/www/upload/"
   }
}
```
### matterbridge configuration
configuration needs to happen in `[general]`
```
[general]
MediaServerUpload="https://a_user:a_password@yourserver.com/upload"
MediaServerDownload="https://yourserver.com/"
```

# Use local download
In this case we're using matterbridge to download to a local path your webserver has read access to and matterbridge has write access to.
Matterbridge is running on this same server.

## matterbridge configuration

In this example the local path matterbridge has write access to is `/var/www/matterbridge`

Your server (apache, nginx, ...) exposes this on `http://yourserver.com/matterbridge` (nginx, apache configuration is out of scope)

configuration needs to happen in `[general]`
```
[general]
MediaDownloadPath="/var/www/matterbridge"
MediaServerDownload="https://yourserver.com/matterbridge"
```

When using the local download configuration, matterbridge does not clean up any of the content it downloads to the Mediaserver path. 
## Sidenote
If you run into issues with the amount of storage available, then it is advised to do an automated cleanup which is to be done externally (i.e. via cron). An example of a clean up script and two examples of cron jobs are provided below. These represent the minimal amount of effort needed to handle this and don't take into account any ability to customize much.

cleanup.sh:
```
#!/bin/bash
find /path/to/matterbridge/media -mindepth 1 -mtime +30 -delete
```

This will delete all downloaded content that is more than 30 days old (but not the media directory itself, due to the use of `-mindepth 1`). You should adjust the path and the max age to suit your own needs. It may be helpful to look at the [`find` manual page](https://www.gnu.org/software/findutils/manual/html_node/find_html/index.html).

To run the script as the user running matterbridge, execute `crontab -e` and add the following line to the bottom of the file:
```
@daily /path/to/cleanup.sh
```

If you want to run it as root, you probably shouldn't (fix your file permissions instead).  However, you can add the script to /etc/cron.daily:
`cp /path/to/cleanup.sh /etc/cron.daily`. This will execute it daily automatically in most Redhat and Debian based Linux distros.