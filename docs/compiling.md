# Building from source

This page documents how to build matterbridge from source. If you're looking for ready-to-use executables, head over to [setup.md].

If you really want to build from source, follow these instructions:
Go 1.18+ is required. Make sure you have [Go](https://golang.org/doc/install) properly installed.

Building the binary with **all** the bridges enabled needs about 3GB RAM to compile.
You can reduce this memory requirement to 0,5GB RAM by adding the `nomsteams` tag if you don't need/use the Microsoft Teams bridge.

Matterbridge can be build without gcc/c-compiler: If you're running on windows first run `set CGO_ENABLED=0` on other platforms you prepend `CGO_ENABLED=0` to the `go build` command. (eg `CGO_ENABLED=0 go install github.com/matterbridge-org/matterbridge`)

To install the latest stable run:

```bash
go install github.com/matterbridge-org/matterbridge
```

To install the latest dev run:

```bash
go install github.com/matterbridge-org/matterbridge@master
```

To install the latest stable run without msteams or zulip bridge:

```bash
go install -tags nomsteams,nozulip github.com/matterbridge-org/matterbridge
```

You should now have matterbridge binary in the ~/go/bin directory:

```bash
$ ls ~/go/bin/
matterbridge
```
