## Command Output

Protect context usage. **Any command with unknown or potentially large output must be byte-capped.**

Default pattern:

```bash
COMMAND 2>&1 | head -c 4000
```

## When stuck
- 如果是go的版本问题，尝试用gvm切换到合适的版本。
- 如果是node.js的版本问题，尝试用nvm切换到合适的版本。
- 提个问题、建议个计划，或开个带注释的草稿 PR。
- 别未经确认就推进大改动。