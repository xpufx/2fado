# TOCTOU mitigation: suspended execution (SIGSTOP/SIGCONT)

Part of the TOCTOU cluster under `#18`. Stops a single-threaded agent
racing the operator during the review window. Not a sandbox.

## Mechanism

1. At petition time the daemon reads the socket peer PID via
   `SO_PEERCRED` and resolves its process group with `Getpgid`.
2. After persisting the pending record and before paging the operator,
   it sends `SIGSTOP` to `-pgid` and records the pin
   (`PendingRecord.Suspend`: pid, pgid, suspended).
3. The hold spans the whole review, including the confirm step, which
   inherits the pin on its child record.
4. On verdict, timeout, or client abort the daemon sends `SIGCONT` to
   `-pgid` (deferred, so every exit path resumes). Detached petitions
   own the barrier in their background waiter, since the parent
   returns `pending` immediately.
5. After a daemon restart, `AdoptOrphans` best-effort resumes any
   stored pin, so a crash cannot leave agents stopped forever.
   Every stop/resume attempt is audited (`suspend`/`resume`); a failed
   stop never denies the petition.

Opt-out: `TWOFADO_SUSPEND=0` (also `false`/`off`/`no`) disables the
barrier; petitions then behave as before. `List`/`Status` expose the
pin so operators see what is held.

## Limits

- **One group only.** Background tasks, `setsid` children, and
  double-forked daemons live in other process groups and keep
  running. An agent that forks out of its group before the stop
  escapes the freeze.
- **Other UIDs unaffected.** A non-root daemon only signals the
  petition UID; cross-UID petitions skip the barrier (kernel would
  deny them anyway). Root can signal any group.
- **Pre-stop race.** Mutations between `2fado run` and the daemon's
  `SIGSTOP` still land. Pair with fd pinning (`#36`) and git-tree
  drift (`#37`) for the entrypoint and workspace.
- **The client itself stops.** `2fado run` blocks on its socket read
  inside the stopped group; the daemon's `SIGCONT` wakes it to read
  the verdict. Detached clients return before the hold starts.
- **Crash recovery is best-effort.** A wiped state dir loses the pin;
  a still-stopped group then needs a manual `kill -CONT -<pgid>`.
- **Never self-stops.** The daemon's own group, pid 1, and unknown
  peers (pid 0) are always skipped.

## Verification

- `TestStopContGroupFreezesChild`: a `Setsid` child shows `T`
  (stopped) under `/proc` after the stop and leaves it after cont.
- `TestSnapshotResumeRoundTrip`: pin, freeze, audit, resume.
- `TestRunWithPeerNoBarrierStoresNoPin`: peerless callers store no
  pin and run the normal approve path.
