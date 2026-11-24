# Memory usage

The released **Telega v0.1.0** binary used **13.0 MiB RSS** with a chat open in the offline text demo on Linux x86-64. After 30 round trips between two chats, median RSS was **15.6 MiB**.

## Released binaries

Medians from five fresh processes per release. Each run used a 110×32 pseudo-terminal, the built-in demo with 15 chats, and `TUI_GRAPHICS=off`. Telega opened the first chat on launch. The run then returned to the chat list and reopened the chat before sending 30 `Ctrl+N` / `Ctrl+P` pairs. Each switch produced a rendered frame. Demo runs used local sample data and isolated XDG directories.

| Scenario | v0.0.4 RSS | v0.1.0 RSS | v0.1.0 PSS | v0.1.0 anonymous PSS |
| --- | ---: | ---: | ---: | ---: |
| Open chat, 1 second after launch | 13.0 MiB | 13.0 MiB | 13.0 MiB | 1.45 MiB |
| Chat list | 13.1 MiB | 13.2 MiB | 13.2 MiB | 1.57 MiB |
| After 30 round trips | 15.5 MiB | 15.6 MiB | 15.6 MiB | 3.81 MiB |

The five `v0.1.0` runs ranged from 13.0 to 13.2 MiB RSS with a chat open and from 15.5 to 15.7 MiB after switching. The archive checksums were verified before running both releases.

## Images and a running session

The gallery measurements below used Telega’s renderer in a separate Go test binary at commit `5a05aa0`. The synthetic scene contained a chat, a gallery with 12 thumbnails, and three message avatars. Each mode was rendered five times from a fresh process in a 128×29 pseudo-terminal.

| Rendering mode | Median RSS | Median PSS | RSS above text-only fixture |
| --- | ---: | ---: | ---: |
| Text only | 9.5 MiB | 9.5 MiB | — |
| Kitty | 14.7 MiB | 14.7 MiB | +5.1 MiB |
| Sixel | 16.6 MiB | 16.6 MiB | +7.0 MiB |

A separate snapshot of an existing connected session running `v0.0.4+dirty` on the same machine showed **21.7 MiB RSS**, **21.7 MiB PSS**, and **7.45 MiB anonymous PSS**. That snapshot reflects one session with its existing workload.

## How to measure

On Linux, start the offline demo in a terminal:

```sh
TUI_GRAPHICS=off telega --demo
```

From another shell, find that process’s PID and inspect its memory:

```sh
pgrep -a -x telega
awk '/^(Rss|Pss|Pss_Anon):/' /proc/PID/smaps_rollup
awk '/^VmHWM:/' /proc/PID/status
```

Replace `PID` with the demo process ID. Linux reports these fields in KiB. RSS counts all resident pages mapped into Telega, including executable pages. PSS divides shared pages among the processes that map them. Anonymous PSS includes heap and stacks. `VmHWM` is the process’s peak RSS.

Measurements were taken on November 24, 2026 on Linux x86-64 with an Intel Core Ultra 5 338H and 4 KiB pages. All figures cover the Telega process. Terminal graphics memory and FFmpeg subprocesses have their own footprints.
