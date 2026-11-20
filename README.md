# telega

A keyboard-driven Telegram client for Linux and OpenBSD, including the Pomera DM250. It connects to a personal account over MTProto through [gotd](https://github.com/gotd/td).

![The telega offline demo showing the compact chat list, message blocks, reactions, a code snippet, and a quoted reply](docs/screenshot.png)

## Features

- **Navigation.** Vim-style keys work with English and Russian layouts. Switch chats from inside a conversation, browse the Archive, and search loaded messages or Telegram history. Chat lists and history load in pages. The current conversation keeps up to 96 messages in memory.
- **Unread activity.** The chat list shows unread counts, mentions, and manual unread marks. Jump to the first unread message or mark through the selected message. A focused chat at the latest messages is marked read even while you compose.
- **Conversations.** Read forum topics and channel comments. Message blocks show authors, formatted text, code, quotes, and reactions. Jump to a quoted message or a forwarded source when Telegram makes it available. The link picker shows the text from the message and the selected destination.
- **Writing.** Compose multiline messages and code blocks, reply, edit your own messages, and select multiple messages to forward or delete. A failed send keeps its draft and attachment for retry while telega is running.
- **Media.** Paste a screenshot from the clipboard, with your draft as the caption, or attach an image or another file by path. Open links and downloaded attachments with the system opener.
- **Alerts.** Notifications identify the chat, event type, and sender when available. Alerts keep message text private and respect Telegram's mute settings for chats and topics, including archived chats.

## Install

Download the archive for your OS and architecture from [GitHub Releases](https://github.com/danilrwx/telega/releases). The releases include Linux and OpenBSD builds for amd64, arm64, and ARMv7, with SHA-256 checksums. For a Pomera DM250 running OpenBSD, use `telega_openbsd_armv7.tar.gz`.

```sh
tar -xzf telega_linux_amd64.tar.gz
install -d "$HOME/.local/bin"
install -m 755 telega "$HOME/.local/bin/telega"
telega --demo
```

Use the demo to try the interface offline. For an account, get your `api_id` and `api_hash` from **API development tools** at [my.telegram.org](https://my.telegram.org). On first launch, telega prompts for the pair and saves it under `${XDG_DATA_HOME:-$HOME/.local/share}/telega/api`. You can also supply `TELEGA_API_ID` and `TELEGA_API_HASH` together as environment variables.

On first sign-in, run `telega` and scan the QR code using **Settings → Devices → Link Desktop Device** in Telegram on your phone. To sign in with an international phone number and a login code, run `telega --phone`. Either flow may ask for your two-step verification password. The session is stored privately as `gotd.session` in the same data directory and reused on startup.

## Keys

| Where | Keys | Action |
| --- | --- | --- |
| Chat list | `j` / `k`, `l` | Select and open a chat |
| Chat list | `/`, `u` / `U`, `r` | Filter the current page, find the next or previous unread chat, or refresh |
| Chat list | `Tab`, `[` / `]`, `A` | Switch between Chats and Archive, page the list, or archive/unarchive the selected chat |
| Any chat | `Ctrl+N` / `Ctrl+P` | Open the next or previous chat and keep the current draft |
| Conversation | `j` / `k`, `Ctrl+U` / `Ctrl+D`, `gg` / `G` | Select a message, page, or jump to the oldest loaded or newest message |
| Conversation | `/`, `?`, `n` / `N` | Search loaded messages, search chat history on Telegram, or navigate matches |
| Conversation | `u`, `m`, `h` | Go to the first unread message, mark through the cursor as read, or return to chats |
| Conversation | `i`, `e`, `r` / `R` | Compose, edit your message, or reply with text or an image |
| Conversation | `J`, `F`, `Ctrl+O` | Open the quoted message, open the forwarded source when available, or jump back |
| Conversation | `y`, `v`, `o` / `O` | Copy text, reveal spoilers, or open an attachment or link |
| Conversation | `Space`, `f`, `d` / `D` | Select multiple messages, forward, or request deletion for yourself or for everyone |
| Conversation | `a`, `A`, `Ctrl+V` / `s` | Attach an image, attach any regular file, or paste a screenshot from the clipboard |
| Conversation | `z` | Choose a reaction. Choosing the same emoji again removes your reaction |
| Conversation | `t`, `c` | Open forum topics or the selected post's comments. Return with `h` or `Ctrl+O` |
| Conversation | `S` | Take a screenshot of a selected region on Wayland |
| Composer | `Enter`, `Ctrl+J`, `Ctrl+S`, `Esc` | Send or retry, add a line, save a draft and return to chats, or cancel |
| Composer with image | `Ctrl+B` | Switch between a Telegram photo and a file with the original bytes |

Terminal paste (often `Ctrl+Shift+V`) keeps line breaks and indentation. Use triple backticks on their own lines to send a code block. A language can follow the opening backticks.

History loads in advance when about half the conversation pane is empty. The selected message stays in place. Timestamps are in UTC.

- Custom emoji reactions appear as a text count.
- Forwarding and deletion ask for confirmation. Telegram applies channel and supergroup deletion to everyone.
- Pasting a screenshot from the clipboard uses `wl-paste` on Wayland or `xclip` on X11.
- To take a screenshot from inside telega on Wayland, press `S` to select a region. This optional shortcut uses `slurp` and `grim`.
- Copying uses `wl-copy`, `xclip`, or OSC 52 when supported by the terminal. Downloads go to the private `telega/attachments` directory in the system cache and open through `open` or `xdg-open`.
- Desktop alerts cover new messages and unread reactions to your messages. When you can already see the message in the focused terminal, its desktop alert is skipped. If `notify-send` is missing or fails, telega rings the terminal bell and shows the alert in its status line.

After a failed send, press `Enter` to retry. If the connection closes, press `r` to reconnect and return to the draft.

## Forum topics and channel comments

1. Open the group with `Enter` or `l`, then press `t` to list its topics.
2. Pick a topic with `j` / `k` or the arrow keys and open it with `Enter`. Use `[` / `]` to page through the topic list.
3. Inside the topic, use the usual history keys. Press `i` to write to the topic or `r` to reply to the selected message. Press `h` to return to the group and `t` to choose another topic.

For a channel post with comments, select it and press `c`. Press `h` to return to the post. `Ctrl+O` retraces any message jumps first, then returns to the parent conversation.

## Build

Use Go 1.26 or later and `make`. The build uses one compiler worker and disables cgo. CI uses Go 1.27.3.

```sh
make
make check
./telega --demo
```

Run `make dm250` to cross-build `telega-dm250` for OpenBSD/ARMv7. Building on a faster machine can help on the Pomera because compiling the generated Telegram schema takes considerably more memory than running the client.

See [CONTRIBUTING.md](CONTRIBUTING.md) for the lint and release commands. telega is licensed under the [Apache License 2.0](LICENSE).
