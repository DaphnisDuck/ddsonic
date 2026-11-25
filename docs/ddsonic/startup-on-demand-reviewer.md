# startup.py: on-demand Reviewer adaptation (proposed, not applied)

Refs #189. `~/.local/share/ddsonic-dev/startup.py` (the dev VM's recovery helper, outside this repo) assumes a persistent Reviewer. With the Reviewer on demand (`ddnetmirror-herdr review`, see `CLAUDE.md`, "Review loop"), the unmodified helper fails or resumes a retired Reviewer; this page records the verified behaviour and a tested patch. The patch is a reviewable artifact only: nothing here changes the live file, and applying it needs the owner's go-ahead.

## What the patch does

- The Reviewer is on demand: never resumed, never validated or published. `plan` returns `ondemand` for it when its pane is a plain shell (or its tab is missing), so `--check`, a normal run and `--stop` accept it.
- Stale Reviewer metadata is ignored, not trusted: a `reviewer` entry in `startup-sessions.json` and the `reviewer-session` file are dropped on load, and `remember()` then writes `startup-sessions.json` without the Reviewer. The `reviewer-session` file and the Codex history are left untouched (`--stop` copies the file into its backup when present).
- Migration state: a saved Herdr layout that still marks the Reviewer pane as a managed Codex agent (the shape of the existing `native-restore-*.json` files) no longer blocks recovery. That pane is stripped of its managed-agent fields so Herdr restores it as a plain shell, with no session or history check; Dispatcher and Writer validation is unchanged. A Reviewer pane in an unexpected shape (wrong directory or agent kind) still stops the restore.
- A live Reviewer agent (the old persistent one) is refused, fail closed: `reviewer: a live agent exists but the reviewer is on demand; retire it first. Left running.` Retire it first (owner step in #189); `--stop` of the old team before that is done with the unmodified helper.
- `--stop` expects exactly the Dispatcher and Writer agents and no longer requires a Reviewer session.

## Apply, check, roll back

Original: sha256 `1101275855f993d37a425a65e453244c934d43d5021a9bd62f0f79d2e9a8f628`. Patched: sha256 `000f87926703690654872a187db4ad864b6f587323c1cf43b0e9460e32f8eb8b`. Run these on the VM with the Reviewer retired and the team stopped or idle; nothing here starts or stops an agent.

```sh
cd ~/.local/share/ddsonic-dev
sha256sum startup.py                          # must equal the original hash above
cp -p startup.py startup.py.pre-ondemand      # rollback copy, kept
awk '/^```diff$/{f=1;next} /^```$/{f=0} f' ~/Documents/projects/ddsonic/docs/ddsonic/startup-on-demand-reviewer.md > /tmp/ondemand.patch
patch --dry-run startup.py < /tmp/ondemand.patch && patch startup.py < /tmp/ondemand.patch
sha256sum startup.py                          # must equal the patched hash above
python3 startup.py --check                    # read-only: Dispatcher/Writer running, "reviewer: on demand"
```

Rollback: `cp -p ~/.local/share/ddsonic-dev/startup.py.pre-ondemand ~/.local/share/ddsonic-dev/startup.py` (then check the original hash). Nothing else is modified, so no state needs undoing.

## How it was tested

Offline, on the dev VM, with the real `Startup` class driven against a stub `herdr` and a temporary HOME (no live Herdr, panes, agents or files under `~/.local/share/ddsonic-dev` were touched; the saved `native-restore-*.json` was read as a layout template). The harness is below. Run it on the original and the patched file:

```sh
mkdir sim && cd sim   # extract the two files below as sim2.py and herdr_stub.py (chmod +x herdr_stub.py)
REAL_LAYOUT=~/.local/share/ddsonic-dev/native-restore-*.json STARTUP=/path/to/startup.py python3 -I sim2.py
```

| Scenario | Original | Patched |
|---|---|---|
| Saved layout with a managed Reviewer, stale metadata (migration) | `Saved Herdr layout cannot prove exact native recovery; server left stopped.` | OK; no managed panes left, layout backed up |
| `--check`, Reviewer pane a plain shell, no Reviewer metadata | `reviewer: no valid saved session ID; refusing a fresh replacement.` | OK, `reviewer: on demand` |
| Run with stale Reviewer in `startup-sessions.json`, `reviewer-session` and Codex history | resumes the old Reviewer into the pane | OK; no agent started; saved sessions no longer hold a Reviewer |
| Run, Dispatcher and Writer crashed, stale Reviewer metadata | starts all three, including the Reviewer | starts Dispatcher and Writer only |
| Run, Dispatcher and Writer crashed, no Reviewer metadata | nothing recovered (fails closed) | starts Dispatcher and Writer only |
| `--check` with a legacy live Reviewer agent | `reviewer: foreground Codex identity is ambiguous; left running.` | `reviewer: a live agent exists but the reviewer is on demand; retire it first. Left running.` |

Not tested: `--stop` (its preflight reads `/proc` and the live Herdr state); the patched lines there are covered by reading only. Review those before applying, and prefer a first `--check` over a stop.

## Patch

```diff
--- startup.py.orig
+++ startup.py
@@ -14,6 +14,10 @@
 import uuid

 ROLES = {'dispatcher': 'claude', 'writer': 'claude', 'reviewer': 'codex'}
+# The Reviewer is on demand: a fresh `codex exec` per round in a plain shell pane
+# (ddnetmirror-herdr review). It is never resumed, and any saved Reviewer session
+# (reviewer-session, startup-sessions.json, a saved managed-agent layout) is stale.
+ON_DEMAND = {'reviewer'}

 class RecoveryError(RuntimeError):
     pass
@@ -134,14 +138,15 @@
             raise RecoveryError('Herdr version/protocol needs lifecycle validation before stopping.')
         inventory = tuple(snapshot[k] for k in ('workspaces', 'tabs', 'panes', 'agents'))
         wid, plan = self.plan(inventory)
-        if len(set(self.sessions.get(role) for role in ROLES)) != 3:
+        live_roles = [role for role in ROLES if role not in ON_DEMAND]
+        if len(set(self.sessions.get(role) for role in live_roles)) != len(live_roles):
             raise RecoveryError('Role session IDs are missing or duplicated; refusing stop.')
         if len(inventory[0]) != 1 or not wid:
             raise RecoveryError('Unexpected workspace(s); refusing session-wide shutdown.')
         agents = inventory[3]
-        if len(agents) != 3 or any(a.get('name') not in ROLES for a in agents):
-            raise RecoveryError('Expected exactly the three named agents; refusing stop.')
-        if any(action != 'keep' for _, action, _, _ in plan):
+        if len(agents) != len(live_roles) or any(a.get('name') not in live_roles for a in agents):
+            raise RecoveryError('Expected exactly the live named agents; refusing stop.')
+        if any(action not in ('keep', 'ondemand') for _, action, _, _ in plan):
             raise RecoveryError('Incomplete team; recover/inspect before stopping.')
         if any(a.get('agent_status') not in ('idle', 'done') or a.get('interactive_ready') is not True
                for a in agents):
@@ -309,6 +314,8 @@
             records[role] = dict(path=str(path), size=len(content), sha256=hashlib.sha256(content).hexdigest())
         for path in (self.state / 'startup-sessions.json', self.state / 'reviewer-session',
                      self.home / '.config/herdr/session.json'):
+            if path.name == 'reviewer-session' and not path.exists():
+                continue  # stale designation: copied for the record if present
             if not path.is_file():
                 raise RecoveryError(f'Required recovery state unavailable: {path}')
             dest = backup / path.name
@@ -362,9 +369,10 @@
             if data.get('repo') != str(self.repo):
                 raise RecoveryError('Recovery record belongs to another repository.')
             self.sessions = data.get('sessions', {})
-        reviewer = self.state / 'reviewer-session'
-        if reviewer.exists():
-            self.sessions['reviewer'] = reviewer.read_text().strip()
+        # Stale Reviewer designations are ignored (the files are left untouched);
+        # remember() then writes startup-sessions.json without them.
+        for role in ON_DEMAND:
+            self.sessions.pop(role, None)

     def remember(self, agents):
         for a in agents:
@@ -432,6 +440,16 @@
             slots = [t for t in tabs if t['workspace_id'] == wid and t.get('label', '').lower() == role]
             if len(live) > 1 or len(slots) > 1:
                 raise RecoveryError(f'{role}: duplicate agents or role tabs; refusing changes.')
+            if role in ON_DEMAND:
+                if live:
+                    raise RecoveryError(f'{role}: a live agent exists but the {role} is on demand; '
+                                        'retire it first. Left running.')
+                if slots:
+                    tab_panes = [p for p in panes if p['tab_id'] == slots[0]['tab_id']]
+                    if len(tab_panes) != 1 or not self.shell_available(tab_panes[0]):
+                        raise RecoveryError(f'{role}: existing tab is busy or split; left untouched.')
+                result.append((role, 'ondemand', None, None))
+                continue
             if live:
                 agent = live[0]
                 matches = [t for t in tabs if t['tab_id'] == agent['tab_id']]
@@ -495,6 +513,13 @@
                         continue
                     role = pane.get('agent_name')
                     meta = pane.get('agent_session') or {}
+                    if role in ON_DEMAND:
+                        # Stale managed Reviewer from before the on-demand model: strip it
+                        # (no history or session check), but only for the expected shape.
+                        if pane.get('cwd') != str(self.repo) or pane.get('managed_agent_kind') != ROLES[role]:
+                            raise RecoveryError('Saved Herdr layout has an unexpected Reviewer pane; server left stopped.')
+                        managed.append(pane)
+                        continue
                     if (role not in ROLES or pane.get('cwd') != str(self.repo)
                             or pane.get('managed_agent_kind') != ROLES[role]
                             or meta.get('kind') != 'id' or meta.get('agent') != ROLES[role]
@@ -578,26 +603,25 @@
         self.ensure_server(check)
         inventory = self.inventory()
         wid, plan = self.plan(inventory)
-        if any(role == 'reviewer' and action == 'keep' for role, action, _, _ in plan):
-            self.validate_live_reviewer(inventory)
         if check:
             for role, action, _, _ in plan:
-                print(f'{role}: {"running" if action == "keep" else "needs recovery"}')
-            if any(action != 'keep' for _, action, _, _ in plan):
+                print(f'{role}: {"running" if action == "keep" else "on demand" if action == "ondemand" else "needs recovery"}')
+            if any(action not in ('keep', 'ondemand') for _, action, _, _ in plan):
                 raise RecoveryError('Team recovery is needed; run ddsonic-dev.')
             return
         # Only record identities after all directories, roles and layouts validate.
-        if any(role == 'reviewer' and action == 'keep' for role, action, _, _ in plan):
-            self.publish_reviewer_identity(inventory)
         self.remember(inventory[3])
         for role, action, pane, args in plan:
+            if action == 'ondemand':
+                print(f'{role}: on demand (plain shell pane; no agent started)')
+                continue
             if action == 'keep':
                 print(f'{role}: preserved')
                 continue
             # Re-read before each mutation; external manual changes must not be duplicated.
             current_wid, current_plan = self.plan(self.inventory())
             current = next(item for item in current_plan if item[0] == role)
-            if current[1] == 'keep':
+            if current[1] in ('keep', 'ondemand'):
                 print(f'{role}: already restored')
                 continue
             wid, (_, _, pane, args) = current_wid, current
@@ -616,10 +640,8 @@
             print(f'{role}: resumed saved session')
         final = self.inventory()
         _, final_plan = self.plan(final)
-        if any(item[1] != 'keep' for item in final_plan):
+        if any(item[1] not in ('keep', 'ondemand') for item in final_plan):
             raise RecoveryError('Recovery incomplete; existing tabs retained for inspection.')
-        self.validate_live_reviewer(final)
-        self.publish_reviewer_identity(final)
         self.remember(final[3])


```

## Harness

```python sim2.py
"""Offline scenarios for startup.py with an on-demand Reviewer (no live Herdr, temp HOME)."""
import importlib.util, json, os, sys, tempfile, uuid
from pathlib import Path
spec = importlib.util.spec_from_file_location("startup", os.environ["STARTUP"])
m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
here = Path(__file__).parent
REAL_LAYOUT = os.environ.get("REAL_LAYOUT")  # a saved native-restore-*.json (read-only)
roles = ["dispatcher", "writer", "reviewer"]

def build(crashed=False, live_reviewer=False, stale=(), busy_reviewer=False):
    home = Path(tempfile.mkdtemp(prefix="sim-home-"))
    repo = home / "Documents/projects/ddsonic"; repo.mkdir(parents=True)
    state = home / ".local/share/ddsonic-dev"; state.mkdir(parents=True)
    r = str(repo)
    ids = {x: str(uuid.uuid4()) for x in roles}
    sessions = {k: ids[k] for k in ("dispatcher", "writer")}
    if "startup-sessions" in stale: sessions["reviewer"] = ids["reviewer"]
    (state / "startup-sessions.json").write_text(json.dumps({"repo": r, "sessions": sessions}))
    if "reviewer-session" in stale: (state / "reviewer-session").write_text(ids["reviewer"] + "\n")
    if "history" in stale:
        d = home / ".codex/sessions/2026/11/01"; d.mkdir(parents=True)
        (d / f"rollout-x-{ids['reviewer']}.jsonl").write_text(json.dumps(
            {"type": "session_meta", "payload": {"id": ids["reviewer"], "cwd": r}}) + "\n")
    h = home / ".claude/projects" / r.replace("/", "-"); h.mkdir(parents=True)
    for x in ("dispatcher", "writer"): (h / f"{ids[x]}.jsonl").write_text(json.dumps({"sessionId": ids[x], "cwd": r}) + "\n")
    agents = [] if crashed else [
        {"name": x, "workspace_id": "w1", "tab_id": f"t-{x}", "pane_id": f"p-{x}", "agent": "claude",
         "cwd": r, "foreground_cwd": r, "agent_session": {"kind": "id", "agent": "claude", "value": ids[x]}}
        for x in ("dispatcher", "writer")]
    if live_reviewer:
        agents.append({"name": "reviewer", "workspace_id": "w1", "tab_id": "t-reviewer", "pane_id": "p-reviewer",
                       "agent": "codex", "cwd": r, "foreground_cwd": r,
                       "agent_session": {"kind": "id", "agent": "codex", "value": ids["reviewer"]}})
    world = dict(repo=r, workspaces=[{"workspace_id": "w1", "label": "ddsonic-dev"}],
        tabs=[{"workspace_id": "w1", "tab_id": f"t-{x}", "label": x.title()} for x in roles],
        panes=[{"workspace_id": "w1", "tab_id": f"t-{x}", "pane_id": f"p-{x}", "cwd": r, "foreground_cwd": r} for x in roles],
        agents=agents, busy=busy_reviewer)
    (home / "world.json").write_text(json.dumps(world))
    os.environ["SIM_WORLD"] = str(home / "world.json")
    return home, repo, ids, m.Startup(home=home, command=[str(here / "herdr_stub.py")])

def starts(home):
    log = Path(str(home / "world.json") + ".calls")
    return sorted({c[2] for c in map(json.loads, log.read_text().splitlines()) if c[:2] == ["agent", "start"]}) if log.exists() else []

def run(name, check=False, **kw):
    home, repo, ids, s = build(**kw)
    try: s.run(check=check); out = "OK"
    except Exception as e: out = f"{type(e).__name__}: {e}"
    extra = ""
    if "startup-sessions" in kw.get("stale", ()):
        saved = json.loads((home / ".local/share/ddsonic-dev/startup-sessions.json").read_text())["sessions"]
        extra = f"; saved sessions keep reviewer={'reviewer' in saved}"
    print(f"[{name}] -> {out}\n    agent starts: {starts(home)}{extra}")

def native(name):
    home, repo, ids, s = build(stale=("startup-sessions", "reviewer-session", "history"))
    layout = json.loads(Path(REAL_LAYOUT).read_text().replace("/home/moores/Documents/projects/ddsonic", str(repo)))
    for ws in layout["workspaces"]:
        for tab in ws["tabs"]:
            for pane in tab["panes"].values():
                role = pane.get("agent_name")
                if role: pane["agent_session"]["value"] = ids[role] if role != "reviewer" else str(uuid.uuid4())
    path = home / ".config/herdr/session.json"; path.parent.mkdir(parents=True)
    path.write_text(json.dumps(layout))
    s.load_sessions()
    try: s.prepare_native_restore(); out = "OK"
    except Exception as e: out = f"{type(e).__name__}: {e}"
    after = json.loads(path.read_text())
    managed = [p.get("agent_name") for w in after["workspaces"] for t in w["tabs"] for p in t["panes"].values() if p.get("managed_agent_kind")]
    print(f"[{name}] -> {out}\n    managed panes left in layout: {managed}; backups: {len(list((home/'.local/share/ddsonic-dev').glob('native-restore-*.json')))}")

native("native restore: saved layout with a managed Reviewer, stale metadata")
run("check: on-demand Reviewer, clean state", check=True)
run("run: stale Reviewer in startup-sessions + reviewer-session + history", stale=("startup-sessions", "reviewer-session", "history"))
run("run: crashed Dispatcher+Writer, stale Reviewer metadata", crashed=True, stale=("startup-sessions", "reviewer-session", "history"))
run("run: crashed Dispatcher+Writer, no Reviewer metadata", crashed=True)
run("check: legacy LIVE Reviewer agent (must not be touched)", check=True, live_reviewer=True)
```

```python herdr_stub.py
#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
w = json.loads(Path(os.environ["SIM_WORLD"]).read_text())
a = sys.argv[1:]
with open(os.environ["SIM_WORLD"] + ".calls", "a") as f:
    f.write(json.dumps(a) + "\n")
def reply(r): print(json.dumps({"result": r})); sys.exit(0)
k = a[:2]
if k == ["workspace", "list"]: reply({"workspaces": w["workspaces"]})
if k == ["tab", "list"]: reply({"tabs": w["tabs"]})
if k == ["pane", "list"]: reply({"panes": w["panes"]})
if k == ["agent", "list"]: reply({"agents": w["agents"]})
if k == ["pane", "process-info"]:
    if w.get("busy") and "p-reviewer" in a: reply({"process_info": {"shell_pid": 100, "foreground_processes": [{"pid": 200, "name": "vim", "cwd": w["repo"]}]}})
    reply({"process_info": {"shell_pid": 100, "foreground_processes": [{"pid": 100, "name": "bash", "cwd": w["repo"]}]}})
if k == ["agent", "start"]:
    role, pane = a[2], a[a.index("--pane") + 1]
    kind = a[a.index("--kind") + 1]
    sid = a[a.index("--resume") + 1] if "--resume" in a else None
    w["agents"].append({"name": role, "workspace_id": "w1", "tab_id": "t-" + role, "pane_id": pane, "agent": kind,
                        "cwd": w["repo"], "foreground_cwd": w["repo"], "agent_session": {"kind": "id", "agent": kind, "value": sid}})
    Path(os.environ["SIM_WORLD"]).write_text(json.dumps(w)); reply({})
print(json.dumps({"error": "unsupported " + " ".join(a)})); sys.exit(1)
```
