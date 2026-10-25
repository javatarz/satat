#!/usr/bin/env node
// PreToolUse hook for the Bash tool (#174). Reads the tool call as JSON on stdin and exits 2,
// with the reason on stderr, when the command would skip the repo's git hooks. It matches the
// command text, so it is a guard against habit and prompt injection, not a boundary: see
// "Limits of the hook guard" in docs/design.md. It fails closed on input it cannot parse.
"use strict";

// Quoted strings blanked out, so a flag-looking word inside a commit message is not a flag.
function unquoted(cmd) {
  return cmd.replace(/"(?:\\.|[^"\\])*"|'[^']*'/g, '""');
}

// Splits on shell command separators so each git invocation is looked at on its own.
function segments(cmd) {
  return cmd.split(/&&|\|\||[;&|\n]|\$\(|`|\(|\)/);
}

const raw = [
  [/\bcore\.hooksPath\b/i, "setting core.hooksPath"],
  [/\bGIT_CONFIG_(COUNT|KEY_\d+|VALUE_\d+|PARAMETERS|GLOBAL|SYSTEM|NOSYSTEM)\b/, "overriding git config through the environment"],
  [/\bHUSKY\s*=\s*0\b|\bHUSKY_SKIP_HOOKS\s*=/, "HUSKY hook skipping"],
  [/(^|[\s;&|(])(export\s+|env\s+)?(PRE_COMMIT_ALLOW_NO_CONFIG|SKIP|SKIP_[A-Z_]+|LEFTHOOK|LEFTHOOK_EXCLUDE)\s*=/, "SKIP / PRE_COMMIT_ALLOW_NO_CONFIG / LEFTHOOK hook skipping"],
  [/\bgit\b[^;&|\n]*--no-verify\b/, "--no-verify"],
  [/\bgit\b[^;&|\n]*\b(commit-tree|update-ref|fast-import)\b/, "git plumbing that writes commits without running commit hooks"],
  [/\.git\/hooks\b/, "touching .git/hooks"],
  [/(>|\btee\b|\bsed\s+-i|\bcp\b|\bmv\b|\bln\b|\brm\b)[^;&|\n]*\.git\/config\b/, "writing .git/config"],
  [/(>|\btee\b|\bsed\s+-i|\bcp\b|\bmv\b|\bln\b|\brm\b|\bchmod\b|\btruncate\b)[^;&|\n]*(\.claude\/settings[^\s"']*\.json|\/etc\/claude-code|\/usr\/local\/lib\/satat)/, "writing Claude settings or the lane policy"],
];

function reason(cmd) {
  for (const [re, why] of raw) {
    if (re.test(cmd)) return why;
  }
  // `git commit -n` is --no-verify. Short flags combine (-an, -nm), so look at the flag letters.
  // (`git push -n` is a dry run and is fine.)
  for (const seg of segments(unquoted(cmd))) {
    if (!/\bgit\b(\s+(-[cC]\s+\S+|-\S+))*\s+commit\b/.test(seg)) continue;
    for (const tok of seg.split(/\s+/)) {
      if (/^-[a-zA-Z]*n[a-zA-Z]*$/.test(tok)) return "git commit -n (--no-verify)";
    }
  }
  return null;
}

function main(input) {
  let call;
  try {
    call = JSON.parse(input);
  } catch (e) {
    return "hook input is not JSON";
  }
  const cmd = call && call.tool_input && call.tool_input.command;
  if (typeof cmd !== "string") return call && call.tool_name === "Bash" ? "Bash call without a command" : null;
  return reason(cmd);
}

module.exports = { reason, main };

if (require.main === module) {
  let input = "";
  process.stdin.setEncoding("utf8");
  process.stdin.on("data", (d) => (input += d));
  process.stdin.on("end", () => {
    let why;
    try {
      why = main(input);
    } catch (e) {
      why = `hook error: ${e.message}`;
    }
    if (why) {
      process.stderr.write(
        `Blocked: ${why}. The repo's git hooks are the gate and must run. Fix what the hook reports instead of skipping it.\n`,
      );
      process.exit(2);
    }
  });
}
