#!/usr/bin/env node
// PreToolUse hook for the Bash tool (#174). Reads the tool call as JSON on stdin and exits 2,
// with the reason on stderr, when the command would skip the repo's git hooks. It matches the
// command text, so it is a guard against habit and prompt injection, not a boundary: see
// "Limits of the hook guard" in docs/design.md. It fails closed on input it cannot parse.
"use strict";

// Splits a command into segments (at ; & | ( ) ` and newlines outside quotes) of words with the
// quotes removed, so `git commit "-n"` and `git commit -m "fix -n"` are told apart.
function tokenize(cmd) {
  const segs = [];
  let seg = [];
  let word = null;
  let quote = null;
  const endWord = () => {
    if (word !== null) seg.push(word);
    word = null;
  };
  const endSeg = () => {
    endWord();
    if (seg.length) segs.push(seg);
    seg = [];
  };
  for (let i = 0; i < cmd.length; i++) {
    const c = cmd[i];
    if (quote) {
      if (c === quote) quote = null;
      else if (c === "\\" && quote === '"' && i + 1 < cmd.length) word += cmd[++i];
      else word += c;
    } else if (c === '"' || c === "'") {
      quote = c;
      if (word === null) word = "";
    } else if (c === "\\" && i + 1 < cmd.length) {
      word = (word || "") + cmd[++i];
    } else if (/\s/.test(c) && c !== "\n") {
      endWord();
    } else if (/[;&|()`\n]/.test(c)) {
      endSeg();
    } else {
      word = (word || "") + c;
    }
  }
  endSeg();
  return segs;
}

const SHELLS = /^(?:.*\/)?(?:sh|bash|zsh|dash|ksh|ash)$/;
const ASSIGN = /^([A-Za-z_][A-Za-z0-9_]*)=/;
// Names that skip or reroute hooks. HUSKY only counts as 0, HUSKY=1 is the default.
function badAssign(tok) {
  const m = ASSIGN.exec(tok);
  if (!m) return false;
  const name = m[1];
  if (name === "HUSKY") return /^HUSKY=0$/.test(tok);
  return /^(HUSKY_SKIP_HOOKS|SKIP|SKIP_.*|PRE_COMMIT_ALLOW_NO_CONFIG|LEFTHOOK|LEFTHOOK_EXCLUDE|GIT_CONFIG_.*)$/.test(name);
}

// Letters of git commit's short flags that take a value: the rest of the word is that value
// (`-mn` is the message "n"), so scanning stops there.
const VALUE_FLAGS = "mFCctS";

function gitReason(args) {
  // Global options, then the subcommand.
  let i = 0;
  for (; i < args.length && args[i].startsWith("-"); i++) {
    if (/^-[cC]$/.test(args[i])) {
      i++;
      if (/hookspath/i.test(args[i] || "")) return "setting core.hooksPath";
    }
  }
  const sub = args[i];
  const rest = args.slice(i + 1);
  if (["commit-tree", "update-ref", "fast-import"].includes(sub)) {
    return "git plumbing that writes commits without running commit hooks";
  }
  // git accepts any unambiguous prefix of a long option: --no-veri, --no-verif and so on.
  if (rest.some((t) => /^--no-ve[a-z]*$/.test(t))) return "--no-verify (or an abbreviation of it)";
  if (sub === "commit") {
    for (const t of rest) {
      if (!/^-[a-zA-Z]+$/.test(t)) continue;
      for (const ch of t.slice(1)) {
        if (ch === "n") return "git commit -n (--no-verify)";
        if (VALUE_FLAGS.includes(ch)) break;
      }
    }
  }
  return null;
}

function segmentReason(seg, depth) {
  let i = 0;
  while (i < seg.length && (ASSIGN.test(seg[i]) || ["env", "export", "command", "exec", "nohup", "time", "if", "then", "else", "elif", "do", "while", "until", "!", "{"].includes(seg[i]))) {
    if (badAssign(seg[i])) return "SKIP / HUSKY / PRE_COMMIT_ALLOW_NO_CONFIG / LEFTHOOK or GIT_CONFIG hook skipping";
    i++;
  }
  const cmd = seg[i] || "";
  const args = seg.slice(i + 1);
  if (/^(?:.*\/)?git$/.test(cmd)) return gitReason(args);
  // A shell given a command string, or eval: check what it would run.
  if (depth < 4 && SHELLS.test(cmd)) {
    const c = args.findIndex((t) => /^-[a-z]*c[a-z]*$/.test(t));
    if (c >= 0 && args[c + 1] !== undefined) return reason(args[c + 1], depth + 1);
  }
  if (depth < 4 && cmd === "eval") return reason(args.join(" "), depth + 1);
  return null;
}

const raw = [
  [/\bcore\.hooksPath\b/i, "setting core.hooksPath"],
  [/\bGIT_CONFIG_(COUNT|KEY_\d+|VALUE_\d+|PARAMETERS|GLOBAL|SYSTEM|NOSYSTEM)\b/, "overriding git config through the environment"],
  [/\.git\/hooks\b/, "touching .git/hooks"],
  [/(>|\btee\b|\bsed\s+-i|\bcp\b|\bmv\b|\bln\b|\brm\b)[^;&|\n]*\.git\/config\b/, "writing .git/config"],
  [/(>|\btee\b|\bsed\s+-i|\bcp\b|\bmv\b|\bln\b|\brm\b|\bchmod\b|\btruncate\b)[^;&|\n]*(\.claude\/settings[^\s"']*\.json|\/etc\/claude-code|\/usr\/local\/lib\/satat)/, "writing Claude settings or the lane policy"],
];

function reason(cmd, depth = 0) {
  for (const [re, why] of raw) {
    if (re.test(cmd)) return why;
  }
  for (const seg of tokenize(cmd)) {
    const why = segmentReason(seg, depth);
    if (why) return why;
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
