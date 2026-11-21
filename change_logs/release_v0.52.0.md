<img src="https://raw.githubusercontent.com/globlit/k9s/master/assets/k9s.png" align="center" width="800" height="auto"/>

# Release v0.52.0

## Notes

K9s Reloaded is a fork of [derailed/k9s](https://github.com/derailed/k9s).

Please grab the latest rev and report any issues!

---

## Log View

* Stream lines continuously, without drops between flushes. [3da1613](https://github.com/globlit/k9s/commit/3da16134)
* Coalesce UI updates on chatty multi-pod streams. [b8d5a0c](https://github.com/globlit/k9s/commit/b8d5a0c0) [3da1613](https://github.com/globlit/k9s/commit/3da16134)
* Re-index appended lines only. [tview@a04e0bf](https://github.com/globlit/tview/commit/a04e0bfdf79d545fc74386e8d005f1ab2c970765) [tview@b7fc5e8](https://github.com/globlit/tview/commit/b7fc5e856220eb4d560a30aaf5ac1324d04568dd)
* Fall back to a literal match on invalid filter regexes. [a9313c7](https://github.com/globlit/k9s/commit/a9313c70)

---

## OIDC Login Prompts

Exec credential plugins (kubelogin/OIDC) used to print login URLs straight onto the TUI. K9s now captures their output, which avoid text dpilling on the UI! [c13d10f](https://github.com/globlit/k9s/commit/c13d10f8)

---

## Native FluxCD Actions

New native fluxCD actions are now IN! THe community plugin is not necessary, and you dont need to install any CLI, everything uses annotations and CR statuses to mark resources for reconciliation, suspend/resume etc. [fc44a60](https://github.com/globlit/k9s/commit/fc44a60d)


| Key       | Action              |
|-----------|---------------------|
| `Shift-R` | Reconcile           |
| `Shift-W` | Reconcile w/ Source |
| `Shift-T` | Suspend/Resume      |
| `Shift-F` | Force/Reset         |
| `Shift-Z` | Jump Source         |


---

## Image Scans Cache

Vulnerability scans persist across sessions in `$XDG_CACHE_HOME/k9s/vul-scans`.
Restarts reuse results without pulling images. After a vulnerability DB update, k9s re-matches cached SBOMs instead of re-scanning.
Packages without a version are skipped, as in the grype CLI. [231b3a5](https://github.com/globlit/k9s/commit/231b3a56)
THis improve overall scan performance :)

---

## Maintenance Release!

### Stability

* Recover panics in background goroutines instead of crashing. [a9313c7](https://github.com/globlit/k9s/commit/a9313c70)
* Fix data races in tables, trees, the view stack, history, pulses, the registry, metrics and config. [9c27cf6](https://github.com/globlit/k9s/commit/9c27cf6f) [289e62e](https://github.com/globlit/k9s/commit/289e62e3) [48054ba](https://github.com/globlit/k9s/commit/48054bab)
* Write config files atomically: crashes and concurrent k9s instances no longer truncate them. [48054ba](https://github.com/globlit/k9s/commit/48054bab)
* Run slow actions off the UI goroutine: delete, drain, cp, rollback, manifest apply/delete. [bdd3af8](https://github.com/globlit/k9s/commit/bdd3af83)
* Never leak privileged node shell pods when a shell fails to start. [c13d10f](https://github.com/globlit/k9s/commit/c13d10f8)
* Load valid plugins when one plugin is invalid. [48054ba](https://github.com/globlit/k9s/commit/48054bab)
* Exit non-zero on crash. [7f3a89f](https://github.com/globlit/k9s/commit/7f3a89f7)

### Performance

* Faster connects: longer on-disk discovery cache, health checks through the existing client. [597914d](https://github.com/globlit/k9s/commit/597914d3)
* Stop informers of idle namespaces, and strip managed fields and CRD schemas from caches. [f4b4f73](https://github.com/globlit/k9s/commit/f4b4f73a) [289e62e](https://github.com/globlit/k9s/commit/289e62e3)
* Cache helm configs, custom column specs and metrics lookups. [597914d](https://github.com/globlit/k9s/commit/597914d3) [52ba16d](https://github.com/globlit/k9s/commit/52ba16d1)
* Skip redundant describe and values refreshes. [52ba16d](https://github.com/globlit/k9s/commit/52ba16d1)
* Build with profile-guided optimization (`default.pgo`). [7f3a89f](https://github.com/globlit/k9s/commit/7f3a89f7)

### Operations

* Rotate `k9s.log` at 50MB. [7f3a89f](https://github.com/globlit/k9s/commit/7f3a89f7)
* Set a 2GiB soft memory limit by default. Override with `GOMEMLIMIT`. [7f3a89f](https://github.com/globlit/k9s/commit/7f3a89f7)
* Opt-in profiling: `K9S_CPUPROFILE` and `K9S_PPROF_ADDR` (loopback only). [7f3a89f](https://github.com/globlit/k9s/commit/7f3a89f7)
* Add a race-detector make target and CI job. [5966e11](https://github.com/globlit/k9s/commit/5966e114)

### Fork

* Rename modules to `github.com/globlit/k9s`, `github.com/globlit/tview` and `github.com/globlit/tcell/v2`. [8f6b9c4](https://github.com/globlit/k9s/commit/8f6b9c48) [384b89f](https://github.com/globlit/k9s/commit/384b89f4)
* Check `globlit/k9s` releases for update notices. [8f6b9c4](https://github.com/globlit/k9s/commit/8f6b9c48)
* Install with `go install github.com/globlit/k9s@latest`.

---

## Resolved Issues

* Crashes on invalid filter input and background goroutine panics [a9313c7](https://github.com/globlit/k9s/commit/a9313c70)
* Frozen log view and missing lines on chatty multi-pod deployments [3da1613](https://github.com/globlit/k9s/commit/3da16134)
* Slow startup and reconnects on constrained networks [597914d](https://github.com/globlit/k9s/commit/597914d3)
* TUI corruption from OIDC exec plugin login URLs [c13d10f](https://github.com/globlit/k9s/commit/c13d10f8)

---

<img src="https://raw.githubusercontent.com/globlit/k9s/master/assets/k9s_small.png" width="32" height="auto"/> K9s Reloaded, forked from [derailed/k9s](https://github.com/derailed/k9s) © Imhotep Software LLC. All materials licensed under [Apache v2.0](http://www.apache.org/licenses/LICENSE-2.0)
