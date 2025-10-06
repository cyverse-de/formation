# Claude Code Configuration

This directory contains project-specific configuration for [Claude Code](https://docs.claude.com/en/docs/claude-code/overview).

## Directory Structure

```
.claude/
├── README.md              # This file (version controlled)
├── hooks-example.md       # Example hook configurations (version controlled)
├── commands/              # Project slash commands (version controlled, if present)
├── settings.local.json    # User-specific permissions (NOT version controlled)
└── hooks/                 # Active hook scripts (NOT version controlled)
```

## Version Controlled Files

These files are shared with the team and tracked in git:

### `hooks-example.md`
Example hook configurations for automated linting and code quality checks. This is **documentation only** - the actual hooks go in `~/.config/claude-code/settings.json` or `.claude/hooks/`.

### `commands/` (if present)
Project-specific slash commands that team members can use. These appear with `(project)` suffix in `/help`.

### This README
Documentation about the .claude directory structure.

## NOT Version Controlled

These files are user-specific and excluded via `.gitignore`:

### `settings.local.json`
Contains user-specific permissions like:
- File paths (e.g., `/home/username/...`)
- Approved tool patterns
- User environment settings

**Why excluded:** Contains user-specific paths and preferences that differ between team members.

### `hooks/`
Directory for active hook scripts that run automatically.

**Why excluded:** May contain:
- User-specific paths or credentials
- Environment-specific configuration
- Secrets or API keys

### `*.local.*`
Any files with `.local.` in the name.

**Why excluded:** Convention for user-specific overrides.

## Usage

### Setting Up Automated Linting

1. Review `hooks-example.md` for example configurations
2. Copy the desired hook configuration to `~/.config/claude-code/settings.json`
3. Ensure `jq` and `ruff` are installed (see main README.md)
4. Edit files - hooks will run automatically!

### Creating Slash Commands

1. Create a new file in `.claude/commands/`:
   ```bash
   mkdir -p .claude/commands
   echo "Run all tests with coverage" > .claude/commands/test-coverage.md
   ```

2. Use with `/test-coverage` in Claude Code

See [Slash Commands Documentation](https://docs.claude.com/en/docs/claude-code/slash-commands.md) for more details.

## Security Considerations

⚠️ **Important:** Never commit files containing:
- Absolute file paths specific to your machine
- Credentials or API keys
- Environment-specific secrets
- Personal preferences

The `.gitignore` is configured to prevent common mistakes, but always review before committing.

## Documentation

- [Claude Code Overview](https://docs.claude.com/en/docs/claude-code/overview)
- [Hooks Guide](https://docs.claude.com/en/docs/claude-code/hooks-guide.md)
- [Slash Commands](https://docs.claude.com/en/docs/claude-code/slash-commands.md)
