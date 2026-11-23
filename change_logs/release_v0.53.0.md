<img src="https://raw.githubusercontent.com/globlit/k9s/master/assets/k9s.png" align="center" width="800" height="auto"/>

# Release v0.53.0

## Notes

This release adds resource diagnostics, structured logs, revision diffs and native actions for daily Kubernetes work.

K9s Reloaded is a fork of [derailed/k9s](https://github.com/derailed/k9s), developed with AI coding tools. To support the original project, please consider [sponsoring the upstream maintainer](https://github.com/sponsors/derailed).

---

## Resource Diagnostics

* Show custom resource readiness through row colors and the `Ctrl-Z` faults filter. [19135f7b](https://github.com/globlit/k9s/commit/19135f7b)
* Add a `ROLLOUT` column to Deployments, StatefulSets and DaemonSets. Identify pending and progressing rollouts, paused Deployments, stalled Deployments and quota failures. [bf57f1fa](https://github.com/globlit/k9s/commit/bf57f1fa)
* Show ResourceQuota usage, limits and the highest utilization percentage. Apply warning and critical thresholds, and expose critical quotas. Show CPU and memory defaults, requests and bounds for LimitRanges. [6251cff1](https://github.com/globlit/k9s/commit/6251cff1)
* Open events for the selected resource with `Shift-E`.  Use `Ctrl-Z` to show warnings (Child-resource events are not included). [6c9b27bf](https://github.com/globlit/k9s/commit/6c9b27bf)

---

## Structured JSON Logs

Toggle JSON rendering with `Shift-J`. Non-JSON lines retain plain rendering for now. [49057673](https://github.com/globlit/k9s/commit/49057673)

Filter parsed fields with `-j level=error` or combine predicates with `-j level=error,msg~timeout`.

To enable it at startup and project selected fields, add this to `config.yaml`:

```yaml
k9s:
  logger:
    json: true
    jsonFields: [level, msg, error]
```

Omit `jsonFields` to show all fields. Hidden fields are counted in a `…+N` marker. Skins can customize JSON colors under `k9s.views.logs.json`. [1604f826](https://github.com/globlit/k9s/commit/1604f826)

---

## Revision Diffs

* Press `Shift-D` in ReplicaSets to compare the selection with the Deployment's current template, or mark two ReplicaSets to compare them. Ignore generated pod-template hashes and Deployment bookkeeping annotations.
* Press `Shift-D` in Helm history to compare the selection with the previous stored revision, or mark two revisions. Press `m` in the diff to switch between values and rendered manifests.
* Search colored unified diffs using the existing details-view controls. [00eaa1ba](https://github.com/globlit/k9s/commit/00eaa1ba) [2db120ff](https://github.com/globlit/k9s/commit/2db120ff)

---

## Native Actions

| View | Key | Action |
|------|-----|--------|
| Kubernetes resources | `Shift-E` | Show events for the selected resource |
| Namespaces | `Shift-Q` | Open quotas for the selected namespace |
| Pods / Containers | `Shift-D` | Open a debug container |
| Deployments | `Shift-P` | Pause or resume selected / marked Deployments |
| CronJobs | `s` | Suspend or resume selected / marked CronJobs |
| Logs | `Shift-J` | Toggle JSON rendering |
| ReplicaSets / Helm history | `Shift-D` | Open a revision diff |

* Debug containers use `kubectl debug` with an image and profile form. [2dcbd62f](https://github.com/globlit/k9s/commit/2dcbd62f)
* Bulk pause and suspend actions use one confirmation. If any selected workload is active, pause or suspend all selections; otherwise resume them. [2c923dc7](https://github.com/globlit/k9s/commit/2c923dc7)
* Add **Remove finalizers** to the delete dialog for terminating selections or when Force is enabled. Check object identity and permissions before removing `metadata.finalizers`. Namespace `spec.finalizers` are not supported. [fe9b99ca](https://github.com/globlit/k9s/commit/fe9b99ca)

Native actions take precedence over conflicting plugin shortcuts by default. In writable Deployment views, `Shift-P` now pauses or resumes workloads; use `Shift-O` to choose a sort column.

---

## Layout

* Move a compact `<K9s>` logo to the bottom-right edge, aligned with the breadcrumb row.
* Narrow the cluster information panel and move namespace shortcuts left to give action hints more room.
* Limit namespace shortcuts to five keys, `0` through `4`, while preserving saved favorites. [b9d0490b](https://github.com/globlit/k9s/commit/b9d0490b)

---

## Fixes and Performance

* Keep event and diff reads on the context where the action started. Use a fresh client for CronJob suspend/resume after context changes. [0008f525](https://github.com/globlit/k9s/commit/0008f525) [23b7371d](https://github.com/globlit/k9s/commit/23b7371d) [7fc75417](https://github.com/globlit/k9s/commit/7fc75417)
* Check workload UIDs before state changes and prevent late delete cleanup from affecting another view or context. [09b9865a](https://github.com/globlit/k9s/commit/09b9865a) [b7d6aa50](https://github.com/globlit/k9s/commit/b7d6aa50)
* Apply configured API request timeouts to Helm clients. [2fa23e79](https://github.com/globlit/k9s/commit/2fa23e79)

---

<img src="https://raw.githubusercontent.com/globlit/k9s/master/assets/k9s_small.png" width="32" height="auto"/> K9s Reloaded, forked from [derailed/k9s](https://github.com/derailed/k9s) © Imhotep Software LLC. All materials licensed under [Apache v2.0](http://www.apache.org/licenses/LICENSE-2.0)
