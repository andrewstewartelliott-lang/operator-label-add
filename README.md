# pod-labeler

A Kubernetes operator, built with the [Operator SDK](https://sdk.operatorframework.io/) (Go, v1.42), that watches for **newly-created Pods** and adds this label to any Pod that does not already have it:

```
app.aelliott.io/label: "true"
```

Each time the operator adds the label, it writes a log line.

---

## Contents

- [How it works](#how-it-works)
- [Behaviour in detail](#behaviour-in-detail)
- [Project layout](#project-layout)
- [Prerequisites](#prerequisites)
- [Build, test, and deploy](#build-test-and-deploy)
- [Viewing the logs](#viewing-the-logs)
- [RBAC](#rbac)
- [OLM bundle and scorecard](#olm-bundle-and-scorecard)
- [Limitations and design notes](#limitations-and-design-notes)
- [Uninstalling](#uninstalling)

---

## How it works

```
 ┌──────────────┐  watch Pods (all namespaces)  ┌──────────────────────────────┐
 │ kube-apiserver│ ────────────────────────────▶│ informer cache               │
 └──────────────┘                               │  (controller-runtime)        │
        ▲                                       └──────────────┬───────────────┘
        │                                                      │ Create events only
        │                                                      ▼  (createOnly predicate)
        │                                       ┌──────────────────────────────┐
        │   JSON merge patch:                   │ PodReconciler.Reconcile()    │
        │   metadata.labels[app.aelliott.io/    │  1. get the Pod              │
        │   label] = "true"                     │  2. skip if being deleted    │
        └───────────────────────────────────────│  3. skip if label key exists │
                                                │  4. patch the label          │
                                                │  5. log "added label to pod" │
                                                └──────────────────────────────┘
```

The operator is a single controller (`internal/controller/pod_controller.go`) running inside a controller-runtime **Manager** (`cmd/main.go`). It has no Custom Resource Definition. It acts directly on the built-in `core/v1` `Pod` type. The project was scaffolded with:

```sh
operator-sdk init --domain aelliott.io --repo aelliott.io/operator-label-add --project-name pod-labeler
operator-sdk create api --group core --version v1 --kind Pod --controller=true --resource=false
```

`--resource=false` tells the SDK not to generate a new API type, because `Pod` already exists.

## Behaviour in detail

| Situation | Result |
|---|---|
| New Pod has no labels | Label `app.aelliott.io/label=true` is added; a log line is written |
| New Pod has other labels but not this key | Label is added and the existing labels are kept; a log line is written |
| New Pod already has the key, with **any** value (e.g. `false`, `custom`) | Pod is left unchanged; nothing is logged |
| Pod is updated, or the label is removed later | Ignored: only Create events are handled |
| Pod is being deleted (`deletionTimestamp` set) | Ignored |
| Pod is deleted before the operator reconciles it | Ignored (`NotFound` errors are swallowed) |
| Patch fails for any other reason | Error is logged and the request is requeued with exponential backoff |

### Key pieces of code

**Event filtering.** A `predicate.Funcs` lets only `CreateEvent`s through, so Pod updates (which are very frequent: status changes, readiness, and so on) never trigger a reconcile:

```go
var createOnly = predicate.Funcs{
    CreateFunc:  func(event.CreateEvent) bool { return true },
    UpdateFunc:  func(event.UpdateEvent) bool { return false },
    DeleteFunc:  func(event.DeleteEvent) bool { return false },
    GenericFunc: func(event.GenericEvent) bool { return false },
}
```

**Adding the label.** The controller uses a JSON **merge patch** (`client.MergeFrom`), not an `Update`. The patch sends only the changed label, so it doesn't conflict with the kubelet or other controllers that are writing to the same Pod at the same time, and it needs no `update` permission.

**"If it does not exist."** Only the label **key** is checked. If a Pod already carries `app.aelliott.io/label` with a different value, the operator respects it and does not overwrite it.

## Project layout

```
cmd/main.go                          Manager setup (metrics, health probes, leader election)
internal/controller/
  pod_controller.go                  The reconciler and the create-only predicate
  pod_controller_test.go             envtest-based tests (real kube-apiserver + etcd)
  suite_test.go                      Ginkgo test suite bootstrap
config/
  default/                           Top-level kustomization used by `make deploy`
  manager/                           Controller Deployment
  rbac/                              ClusterRole (generated), bindings, ServiceAccount
  manifests/bases/                   ClusterServiceVersion base used for the OLM bundle
  scorecard/                         Scorecard test configuration
bundle/                              Generated OLM bundle (`make bundle`)
bundle.Dockerfile                    Builds the bundle image
Dockerfile                           Builds the operator image (distroless, non-root)
Makefile                             Build, test, deploy, and bundle targets
PROJECT                              Operator SDK / Kubebuilder project metadata
```

## Prerequisites

- Go 1.24+
- Docker (or Podman)
- `kubectl` and access to a cluster (these instructions use [kind](https://kind.sigs.k8s.io/))
- `operator-sdk` v1.42+ (`brew install operator-sdk`)

`controller-gen`, `kustomize` and `setup-envtest` are downloaded into `./bin` by the Makefile when they are first needed.

## Build, test, and deploy

```sh
# Regenerate RBAC from the +kubebuilder markers, then format and vet the code
make manifests generate fmt vet

# Run the controller tests against a local envtest API server
make test

# Build the image and load it into a kind cluster
make docker-build IMG=pod-labeler:v0.0.1
kind load docker-image pod-labeler:v0.0.1 --name <cluster-name>

# Deploy into namespace pod-labeler-system
make deploy IMG=pod-labeler:v0.0.1
kubectl -n pod-labeler-system rollout status deploy/pod-labeler-controller-manager
```

For a registry-backed cluster, push the image (`make docker-push IMG=<registry>/pod-labeler:<tag>`) and deploy with that `IMG`.

You can also run the controller on your machine against the cluster in your current kubeconfig, without building an image:

```sh
make run
```

### Try it

```sh
kubectl create ns labeler-demo
kubectl -n labeler-demo run no-label  --image=busybox --restart=Never -- sleep 3600
kubectl -n labeler-demo run has-label --image=busybox --restart=Never \
  --labels=app.aelliott.io/label=custom -- sleep 3600

kubectl -n labeler-demo get pods --show-labels
# NAME        READY   STATUS    LABELS
# has-label   1/1     Running   app.aelliott.io/label=custom          <- left alone
# no-label    1/1     Running   app.aelliott.io/label=true,run=no-label  <- label added
```

## Viewing the logs

```sh
kubectl -n pod-labeler-system logs deploy/pod-labeler-controller-manager -f | grep "added label"
```

Each labeled Pod produces one structured log line. controller-runtime adds the Pod's name and namespace automatically:

```
2026-11-19T04:08:08Z	INFO	added label to pod	{"controller": "pod", "controllerGroup": "", "controllerKind": "Pod",
  "Pod": {"name":"fourth","namespace":"labeler-demo"}, "namespace": "labeler-demo", "name": "fourth",
  "reconcileID": "...", "label": "app.aelliott.io/label=true"}
```

Logging uses zap through controller-runtime. To get JSON output or change the level, pass the standard flags to the manager, e.g. `--zap-encoder=json` or `--zap-log-level=debug` (add them to `args` in `config/manager/manager.yaml`).

## RBAC

The ClusterRole is generated from this marker in `pod_controller.go`:

```go
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;patch
```

It gives the least privilege the controller needs: read and watch Pods cluster-wide, and patch them. It does **not** grant `create`, `update`, `delete`, `pods/status` or `pods/finalizers`. The scaffold also includes RBAC for leader election (Leases in the operator's namespace) and for protecting the metrics endpoint.

## OLM bundle and scorecard

The project includes an [Operator Lifecycle Manager](https://olm.operatorframework.io/) bundle, so it can be installed through OLM or published to a catalog.

```sh
make bundle IMG=pod-labeler:v0.0.1          # writes bundle/ and validates it
make bundle-build bundle-push BUNDLE_IMG=<registry>/pod-labeler-bundle:v0.0.1
operator-sdk run bundle <registry>/pod-labeler-bundle:v0.0.1   # install via OLM
```

The CSV declares only the **AllNamespaces** install mode, because the operator watches Pods in every namespace.

### Scorecard

`operator-sdk scorecard` runs the built-in **basic** and **olm** test suites as Pods in the cluster against the bundle:

```sh
operator-sdk scorecard ./bundle -n default -w 180s
```

Results (v1.42.3, kind, Kubernetes v1.36):

| Test | Suite | Result |
|---|---|---|
| basic-check-spec | basic | pass |
| olm-bundle-validation | olm | pass |
| olm-crds-have-validation | olm | pass |
| olm-crds-have-resources | olm | pass |
| olm-spec-descriptors | olm | pass |
| olm-status-descriptors | olm | pass |

Because this operator owns no CRDs, the CRD and descriptor tests pass without anything to check ("Loaded 0 Custom Resources"). The checks that matter here are bundle validation and the CSV checks.

## Limitations and design notes

- **Labels are added after creation, not at admission.** For a short moment (usually milliseconds) a new Pod exists without the label. Anything that selects Pods by this label right at creation could miss it. If the label must be present from the very first moment, use a **mutating admission webhook** (`operator-sdk create webhook ...`) or a policy engine such as Kyverno instead.
- **Startup sync.** When the operator starts, or a new leader takes over, controller-runtime lists all existing Pods and sends each one as a Create event. Pods that were created while the operator was down are therefore also labeled, and so are pre-existing Pods without the label the first time the operator is installed. This is deliberate: the operator never misses a Pod.
- **Leader election** is on by default (`--leader-elect`), so you can safely run more than one replica. `LeaderElectionReleaseOnCancel: true` is set in `cmd/main.go`, so during a rolling update the old Pod gives up the lease straight away and the new one takes over within seconds, without waiting for the full lease duration (about 15–30 seconds). No Pods are lost during a handover; ones created in the gap are labeled by the startup sync.
- **Label removal is not reverted.** Only Create events are handled, so if someone removes the label later, it stays removed. To enforce the label continuously, change the predicate to also pass Update events where the label is missing.
- **Scope.** All namespaces, including `kube-system` and the operator's own namespace. To exclude namespaces, add a check to the predicate (e.g. on `e.Object.GetNamespace()`), or restrict the cache with `cache.Options.DefaultNamespaces` in `cmd/main.go`.
- **Static Pods / mirror Pods.** Kubelet-managed mirror Pods (e.g. control-plane components) are labeled on the API object only. This has no effect on the static Pod manifest on the node.

## Uninstalling

```sh
make undeploy      # removes the namespace, Deployment, RBAC, and Service
```

Labels already added to Pods are not removed.

## License

Apache 2.0. See the headers in the source files.
