## Principles
### 1. think before coding
state your assumptions. ask when unsure. never guess.

### 2. simplicity first
write the minimum code that solves the problem.
no abstractions nobody asked for.

### 3. surgical changes
don't touch code unrelated to the request.
every changed line must trace back to what was asked.

### 4. goal-driven execution
turn vague instructions into verifiable success criteria
before writing a single line.

### 5. Context Window
when context window usages reaches 85%, trigger auto compact by running `/compact` command in codex.

## Command Output

Protect context usage. **Any command with unknown or potentially large output must be byte-capped.**

Default pattern:

```bash
COMMAND 2>&1 | head -c 4000
```

## FAQ
- 如果是go的版本问题，尝试用 gvm use go1.25.5 切换到合适的版本。
- 如果是node.js的版本问题，尝试用 nvm 切换到合适的版本。
- 如果遇到任何问题，你处理不了卡住了，就向我提问题、建议个计划，或开个带注释的草稿 PR，别未经确认就推进大改动。
- 执行 curl 的 GET METHOD 命令不需要向我确认。