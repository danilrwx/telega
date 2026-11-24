# Telega

**Telegram for your terminal.** Catch up on unread chats, find anything in your history, and reply with Vim-style keys. Runs on Linux and OpenBSD, including the Pomera DM250.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/danilrwx/telega/main/scripts/install.sh | sh
```

The installer picks the right build, checks its SHA-256 hash, and installs it to `~/.local/bin`. [Download an archive instead](https://github.com/danilrwx/telega/releases/latest).

```sh
~/.local/bin/telega --demo
```

Try sample chats offline before signing in.

![Telega chat and media gallery side by side. The chat shows unread markers, avatars, an inline image, a voice transcript, reactions, a quoted reply, and a saved draft.](docs/demo-overview.png)

## What you can do

- **Find it fast.** Jump to a chat by name or username. Search messages across Telegram or browse a chat’s full media history.
- **Handle voice messages in place.** Record, play, pause, and read Telegram’s transcription beneath the message.
- **Make the terminal yours.** Keep drafts when you quit, edit in Vim, and see photos and avatars in Kitty or Sixel terminals.

## Sign in

Get your API ID and hash from **API development tools** at [my.telegram.org](https://my.telegram.org). Run `~/.local/bin/telega` and scan the QR code in Telegram under **Settings → Devices → Link Desktop Device**. Use `--phone` for phone-number sign-in.

[Keyboard shortcuts and setup](https://github.com/danilrwx/telega/blob/main/docs/usage.md) · [Releases](https://github.com/danilrwx/telega/releases) · [Build from source](https://github.com/danilrwx/telega/blob/main/CONTRIBUTING.md)
