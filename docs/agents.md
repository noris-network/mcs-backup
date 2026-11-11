# AI agents and the command line for scripts

## MCP server

`mcs-backup mcp` speaks the [Model Context Protocol][mcp] on stdin and stdout
and talks to the running service over its control socket. It starts nothing on
its own; the agent (Claude Code, Claude Desktop, any MCP client) connects to it:

```console
# in a pod
kubectl exec -i <pod> -c mcs-backup -- mcs-backup mcp
kubectl exec -i <pod> -c mcs-backup -- mcs-backup mcp --allow-write

# on a host
mcs-backup mcp --allow-write
```

Example for an MCP client configuration:

```json
{
  "mcpServers": {
    "backup-shop": {
      "command": "kubectl",
      "args": ["exec", "-i", "-n", "shop", "deploy/shop", "-c", "mcs-backup", "--", "mcs-backup", "mcp", "--allow-write"]
    }
  }
}
```

### Access levels

What an agent can do is decided by whoever starts the server, not by the agent.
Tools above the chosen level are not registered at all, so the agent never sees them.

| Level | Flag | Tools |
| --- | --- | --- |
| read | (default) | `get_status`, `list_snapshots`, `get_retention`, `get_restore_preflight`, `get_operation` (with `wait_seconds`), `get_log`, `list_locks`, `list_files`, `find_files`, `get_snapshot_diff`, `get_restic_env` (names only) |
| write | `--allow-write` | `start_backup`, `start_verify`, `cancel_operation`, `keep_snapshot`, `unkeep_snapshot`, `unlock_stale_locks`, `set_schedule` (enable, disable, maintenance), `test_notification`, `restore_to_directory` (new or empty directories only) |
| destructive | `--allow-destructive` (implies write) | `restore_overwrite` (live data or non-empty directories), `acknowledge_snapshot`, `approve_forget`, `unlock_all_locks` |

With an [append-only](append-only.md) repository the tools that change tags
(`keep_snapshot`, `unkeep_snapshot`, `acknowledge_snapshot`) and
`approve_forget`/`unlock_all_locks` are not offered.

The tools that can lose data need a `confirm` argument that repeats what is
being done (the snapshot for a restore, `acknowledge`, `forget`, `remove-all`).
The server instructions tell the agent to use them only when the user asked for
exactly that.

Operations take time, so `start_*` tools return at once; the agent waits with
`get_operation` and its `wait_seconds` argument. A second operation while one
runs is rejected with the name of the running one.

### What this is, and what it is not

* **Secrets are never returned**: `get_restic_env` lists variable names and
  notes, not values, and logs are redacted.
* The levels are **guard rails for well-meaning agents, not a security
  boundary**. Whoever may run `kubectl exec` or open a shell can read the
  environment of the process, run `mcs-backup env`, and do anything the command
  line can. Give an agent access to the MCP server the way you would give a
  colleague a shell.
* Every action is logged with its source (`mcp`).
* There are no web UI tools; the web UI is not reachable through MCP.

## Command line for scripts

All client commands accept `--json` and print the same structures as the API.
Exit codes are fixed:

| Code | Meaning |
| --- | --- |
| 0 | success (also: a warning, check the JSON `result`) |
| 1 | the operation failed |
| 2 | wrong usage or configuration, including a missing or wrong confirmation |
| 3 | the service is not running or the socket is not reachable |
| 4 | another operation is running (the message names it) |

```console
$ mcs-backup status --json | jq '.warnings'
$ mcs-backup backup --note "before upgrade" --keep --json
$ mcs-backup restore latest --confirm             # in place; without a TTY --confirm is required
```

`status --follow` streams state changes, `backup --no-wait` returns once the
backup has started.

[mcp]: https://modelcontextprotocol.io
