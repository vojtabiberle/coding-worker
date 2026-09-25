// Bundled worker policy. No discovered extensions are loaded alongside this file.
import fs from "node:fs";
import path from "node:path";

export default function (pi) {
  const policy = JSON.parse(fs.readFileSync(new URL("./policy.json", import.meta.url), "utf8"));
  const root = fs.realpathSync(policy.root);
  const emit = (type, extra = {}) => fs.writeSync(1, JSON.stringify({ type, ...extra }) + "\n");
  const fail = (message) => { emit("worker_error", { message }); process.exit(78); };
  let requests = 0;
  pi.on("session_start", () => emit("worker_ready"));
  pi.on("before_provider_request", (_event, ctx) => {
    if (!ctx.model || `${ctx.model.provider}/${ctx.model.id}` !== policy.model) fail("Pi resolved a different provider/model than the profile");
    if (++requests > policy.maxSteps) fail("Pi max_steps exhausted before next provider request");
  });
  pi.on("tool_call", (event) => {
    if (!policy.readOnly) return;
    if (!["read", "grep", "find", "ls"].includes(event.toolName)) {
      return { block: true, reason: "Worker investigation permits only read, grep, find and ls" };
    }
    const input = event.input;
    const candidate = input.path ?? (event.toolName === "read" ? null : root);
    if (typeof candidate !== "string" || candidate.includes("\0")) return { block: true, reason: "Invalid path" };
    try {
      const resolved = fs.realpathSync(path.resolve(root, candidate));
      const relative = path.relative(root, resolved);
      if (relative === ".." || relative.startsWith(".." + path.sep) || path.isAbsolute(relative)) {
        return { block: true, reason: "Path outside the worktree" };
      }
      // Prevent reads from devices, sockets, FIFOs and procfs through special paths.
      const stat = fs.statSync(resolved);
      if (!stat.isFile() && !stat.isDirectory()) return { block: true, reason: "Not a regular file or directory" };
    } catch {
      return { block: true, reason: "Path cannot be resolved inside the worktree" };
    }
  });
}
