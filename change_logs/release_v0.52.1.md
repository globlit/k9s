# Release v0.52.1

- Stop background workers when the app closes. [8c7b60c6](https://github.com/globlit/k9s/commit/8c7b60c6)
- Prevent action-map races during UI callbacks. [a259f556](https://github.com/globlit/k9s/commit/a259f556)
- Reject invalid shell pod resource limits before creating a pod. [50018fa9](https://github.com/globlit/k9s/commit/50018fa9)
- Honor workload label expressions and reject missing selectors. [5a6160d8](https://github.com/globlit/k9s/commit/5a6160d8)
- Report pod access denials correctly. [88a3b7f3](https://github.com/globlit/k9s/commit/88a3b7f3)
- Preserve port-forward sessions during namespace navigation and hide unsupported RBAC actions. [923de530](https://github.com/globlit/k9s/commit/923de530)
- Preserve namespaces when merging empty fuzzy filters. [e0e338b3](https://github.com/globlit/k9s/commit/e0e338b3)
