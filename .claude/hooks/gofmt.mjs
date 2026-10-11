// PostToolUse hook for Edit/Write: gofmt the edited Go file (CI enforces
// `gofmt -l .`). Syntax errors are reported back to Claude with exit 2.
import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';

const { tool_input: input = {} } = JSON.parse(readFileSync(0, 'utf8'));
const path = input.file_path ?? '';
if (!path.endsWith('.go')) process.exit(0);

const r = spawnSync('gofmt', ['-w', path], { encoding: 'utf8' });
if (r.error) process.exit(0); // gofmt not on PATH: CI will still catch it
if (r.status !== 0) {
  process.stderr.write(`gofmt failed on ${path}:\n${r.stderr}`);
  process.exit(2);
}
