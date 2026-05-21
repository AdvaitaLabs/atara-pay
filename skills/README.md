# Atara-Pay Agent Skills

Each subdirectory holds a `SKILL.md` — a self-contained instruction bundle
that an AI agent runtime (Claude Code, Cursor, Cline, …) can load to perform
one concrete Atara-Pay workflow via the `atara` CLI.

Skills are deliberately CLI-only. They do not call the HTTP API directly,
they do not embed API keys, and they do not write code. Every action goes
through `atara <subcommand>` so the local config + auth path is honored.

## Available skills

| Skill | What it does |
| ----- | ------------ |
| [pay-merchant/](pay-merchant/SKILL.md) | Send a one-off payment to a merchant alias or address |
| [provision-agent-wallet/](provision-agent-wallet/SKILL.md) | Create a wallet group + session key for an autonomous agent |
| [onramp-fiat/](onramp-fiat/SKILL.md) | Top up a wallet group from a card via CrossMint |
| [tenant-bootstrap/](tenant-bootstrap/SKILL.md) | First-time setup: keys, webhook, default limits |
| [investigate-violation/](investigate-violation/SKILL.md) | Triage a `limit.exceeded` webhook |

## How to use

1. Install the CLI: `curl -sSf https://get.atara.xyz/install.sh | bash`
2. Point your agent runtime at `~/.atara/skills/`.
3. Ask the agent in natural language — it loads the matching SKILL and runs
   the listed commands.
