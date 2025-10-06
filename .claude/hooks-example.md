# Claude Code Hooks Configuration

This file contains example hook configurations for automatically fixing linting issues in the formation project.

## PostToolUse Hook for Automatic Linting

Add this to your `~/.config/claude-code/settings.json` to automatically run ruff fix after editing Python files:

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Edit|Write",
        "hooks": [
          {
            "type": "command",
            "command": "jq -r '.tool_input.file_path' | { read file_path; if echo \"$file_path\" | grep -q '\\.py$'; then uv run ruff check --fix \"$file_path\" 2>&1 || true; fi; }"
          }
        ]
      }
    ]
  }
}
```

### What This Hook Does:
1. Triggers after every `Edit` or `Write` tool usage
2. Extracts the file path from the tool input
3. Checks if the file is a Python file (`.py` extension)
4. Runs `uv run ruff check --fix` on the file to auto-fix linting issues
5. Continues even if ruff returns an error (`|| true`)

### Benefits:
- ✅ Automatic fixing of unused imports
- ✅ Automatic import sorting
- ✅ Consistent code formatting
- ✅ No manual intervention needed
- ✅ Runs only on Python files

## Alternative: Full Project Lint

For a more comprehensive approach that lints the entire project after edits:

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Edit|Write",
        "hooks": [
          {
            "type": "command",
            "command": "jq -r '.tool_input.file_path' | { read file_path; if echo \"$file_path\" | grep -q '\\.py$'; then uv run ruff check --fix . 2>&1 | head -20 || true; fi; }"
          }
        ]
      }
    ]
  }
}
```

## Pre-commit Git Hook (Optional)

If you want to run ruff before committing, create `.git/hooks/pre-commit`:

```bash
#!/bin/bash
# Run ruff fix on staged Python files

echo "Running ruff on staged Python files..."
git diff --cached --name-only --diff-filter=ACMR | grep '\.py$' | while read file; do
    uv run ruff check --fix "$file"
    git add "$file"
done
```

Make it executable:
```bash
chmod +x .git/hooks/pre-commit
```

## Testing the Configuration

1. Edit a Python file and introduce an issue:
   ```python
   import sys  # unused import

   def foo():
       pass
   ```

2. Save the file
3. Check if ruff automatically removed the unused import
4. If using the hook, you should see no diagnostic issues

## Troubleshooting

If hooks aren't working:

1. **Check hook syntax**: Ensure JSON is valid
2. **Test command manually**:
   ```bash
   uv run ruff check --fix routes/apps.py
   ```
3. **Check Claude Code logs**: Look for hook execution errors
4. **Verify jq is installed**: `which jq`

## Documentation

For more information about Claude Code hooks:
- [Hooks Guide](https://docs.claude.com/en/docs/claude-code/hooks-guide.md)
- [Hooks Reference](https://docs.claude.com/en/docs/claude-code/hooks.md)
