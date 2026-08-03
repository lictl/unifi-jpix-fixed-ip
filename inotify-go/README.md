# inotify-go

A tiny, dependency-free stand-in for `inotifywait`, built for the
[UniFi JPIX Fixed IP script](../network_watcher) service running on a UniFi Dream Machine.

## Why

The service previously depended on `inotifywait` from the `inotify-tools` apt
package. On UniFi OS the root filesystem (including installed apt packages) is
reset on firmware updates — only `/data` persists. A single static Go binary
dropped in `/data/bin` survives updates and removes the runtime dependency.

It uses the Linux inotify syscalls directly through the standard library, so it
has **no third-party dependencies**.

## Scope

Only the subset the service needs is implemented:

| Flag         | Meaning                                                  |
|--------------|----------------------------------------------------------|
| `-m`         | Keep watching after the first event (monitor mode).      |
| `-e`         | Event(s) to watch — comma-separated and/or repeated.     |
| `--format`   | Output template. Supports `%T`, `%e`, `%f`, `%w`.        |
| `--timefmt`  | strftime format used to render `%T`.                     |
| `-q`         | Suppress the startup message on stderr.                  |

Supported `-e` events: `access`, `modify`, `attrib`, `close_write`,
`close_nowrite`, `close`, `open`, `moved_from`, `moved_to`, `move`, `create`,
`delete`, `delete_self`, `move_self`.

`%e` prints comma-separated uppercase names (e.g. `CLOSE_WRITE`, `CREATE,ISDIR`),
matching `inotifywait` so existing log parsing keeps working.

`%T` prints the event time formatted with `--timefmt`, a strftime string. The
common specifiers are supported (`%Y %y %m %d %e %H %I %M %S %p %a %A %b %B %j
%F %T %%`); unknown ones are passed through verbatim. When `--timefmt` is
omitted it defaults to `%Y-%m-%dT%H:%M:%S`.

Example (how the service invokes it):

```sh
inotify-go -m -e moved_to,create,modify,close_write \
  --format '%T %e %f' --timefmt '%Y-%m-%dT%H:%M:%S' /some/dir
```

## Build

The UDM has no Go toolchain, so cross-compile on your dev machine. The default
target is `linux/arm64` (UDM hardware).

```sh
make build # produces ./inotify-go (linux/arm64, static, stripped)
```

Copy the binary to `/data/bin/inotify-go` on the gateway before running
`network_watcher/install.sh`. Public release archives provide prebuilt ARM64
and AMD64 binaries.
