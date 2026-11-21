<img src="https://raw.githubusercontent.com/globlit/k9s/master/assets/k9s.png" align="center" width="800" height="auto"/>

# Release v0.52.0

## Notes

First release of **K9s Reloaded**, a maintained fork of [derailed/k9s](https://github.com/derailed/k9s) focused on stability and speed for daily use on busy clusters.

This release targets crashes, frozen UIs on chatty log streams, slow connects on constrained networks, and screen corruption from OIDC login prompts.
K9s Reloaded now builds on its own forks of [tview](https://github.com/globlit/tview) and [tcell](https://github.com/globlit/tcell).

Please grab the latest rev and report anything odd!

---

## Logs: Fast Again!

Log views no longer stall on busy multi-pod deployments.

* Lines stream continuously, even when a stream never pauses. No more lines dropped between flushes.
* UI updates are coalesced and paced on draw cost, so the view stays responsive at tens of thousands of lines per second.
* The log view re-indexes appended lines only. A frame on a 5k-line buffer drops from ~470ms to ~3.5ms.
* Invalid filter regexes fall back to a literal match instead of crashing k9s.

---

## OIDC Login Prompts

Exec credential plugins (kubelogin/OIDC) used to print login URLs straight onto the TUI.
K9s now captures their output: it shows in the flash bar and the log while the TUI is active, and passes through to the terminal while a shell owns it.

---

## Native FluxCD Actions

No `flux` CLI required. Actions apply to all marked resources.

| Key       | Action              |
|-----------|---------------------|
| `Shift-R` | Reconcile           |
| `Shift-W` | Reconcile w/ Source |
| `Shift-T` | Suspend/Resume      |
| `Shift-F` | Force/Reset         |
| `Shift-Z` | Jump Source         |

The community `plugins/flux.yaml` now only carries `trace` and `list suspended`.

---

## Image Scans Cache

Vulnerability scans persist across sessions in `$XDG_CACHE_HOME/k9s/vul-scans`.
Restarts reuse results without pulling images. After a vulnerability DB update, k9s re-matches cached SBOMs instead of re-scanning.
Packages without a version are skipped, as in the grype CLI.

---

## Maintenance Release!

### Stability

* Recover panics in background goroutines instead of crashing.
* Fix data races in tables, trees, the view stack, history, pulses, the registry, metrics and config.
* Write config files atomically: crashes and concurrent k9s instances no longer truncate them.
* Run slow actions off the UI goroutine: delete, drain, cp, rollback, manifest apply/delete.
* Never leak privileged node shell pods when a shell fails to start.
* Load valid plugins when one plugin is invalid.
* Exit non-zero on crash.

### Performance

* Faster connects: longer on-disk discovery cache, health checks through the existing client.
* Stop informers of idle namespaces, and strip managed fields and CRD schemas from caches.
* Cache helm configs, custom column specs and metrics lookups.
* Skip redundant describe and values refreshes.
* Build with profile-guided optimization (`default.pgo`).

### Operations

* Rotate `k9s.log` at 50MB.
* Set a 2GiB soft memory limit by default. Override with `GOMEMLIMIT`.
* Opt-in profiling: `K9S_CPUPROFILE` and `K9S_PPROF_ADDR` (loopback only).
* Add a race-detector make target and CI job.

### Fork

* Rename modules to `github.com/globlit/k9s`, `github.com/globlit/tview` and `github.com/globlit/tcell/v2`.
* Check `globlit/k9s` releases for update notices.
* Install with `go install github.com/globlit/k9s@latest`.

---

## Resolved Issues

* Crashes on invalid filter input and background goroutine panics
* Frozen log view and missing lines on chatty multi-pod deployments
* Slow startup and reconnects on constrained networks
* TUI corruption from OIDC exec plugin login URLs

---

<img src="https://raw.githubusercontent.com/globlit/k9s/master/assets/k9s_small.png" width="32" height="auto"/> K9s Reloaded, forked from [derailed/k9s](https://github.com/derailed/k9s) © Imhotep Software LLC. All materials licensed under [Apache v2.0](http://www.apache.org/licenses/LICENSE-2.0)
