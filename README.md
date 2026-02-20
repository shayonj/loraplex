# loraplex

A managed adapter storage and routing layer for LoRA adapters on vLLM.

loraplex sits between your clients and vLLM. It manages LoRA adapter files on disk so vLLM doesn't have to — fetching them on demand from HuggingFace, S3, or any HTTP origin, storing them in a size-bounded directory with LRU eviction, and using consistent hashing to route requests to the node that already has the adapter stored. Adapters can also arrive via the filesystem directly (training pipelines, rsync, NFS) and loraplex will detect and serve them. vLLM's `lora_filesystem_resolver` reads adapter files from the same directory loraplex writes to.

- [Quick Start](#quick-start)
  - [Install](#install)
  - [Run (single node)](#run-single-node)
  - [Run (multi-node)](#run-multi-node)
  - [Run (Kubernetes)](#run-kubernetes)
- [How It Works](#how-it-works)
- [API](#api)
  - [Proxy endpoints (passthrough to vLLM)](#proxy-endpoints-passthrough-to-vllm)
  - [Admin endpoints](#admin-endpoints)
- [Examples and Deployment Modes](#examples-and-deployment-modes)
- [Architecture Details](#architecture-details)
  - [Storage](#storage)
  - [Routing](#routing)
  - [Origins](#origins)
  - [Invalidation](#invalidation)
- [Reference](#reference)
  - [CLI Flags](#cli-flags)
  - [Config File](#config-file)
  - [Prometheus Metrics](#prometheus-metrics)
- [Development](#development)
- [License](#license)

## Quick Start

### Install

```bash
go install github.com/shayonj/loraplex/cmd/loraplex@latest
```

Or build from source:

```bash
git clone https://github.com/shayonj/loraplex.git
cd loraplex
make build
```

### Run (single node)

```bash
# Start vLLM with filesystem resolver pointed at loraplex's storage directory
export VLLM_ALLOW_RUNTIME_LORA_UPDATING=true
export VLLM_PLUGINS=lora_filesystem_resolver
export VLLM_LORA_RESOLVER_CACHE_DIR=/mnt/nvme/loraplex

python -m vllm.entrypoints.openai.api_server \
  --model meta-llama/Llama-3.1-8B-Instruct \
  --enable-lora --max-loras 200 --max-lora-rank 64

# Start loraplex (writes adapter files to the same directory vLLM reads from)
./bin/loraplex \
  --listen :9090 \
  --vllm-url http://localhost:8000 \
  --dir /mnt/nvme/loraplex
```

Point clients at `localhost:9090` instead of `localhost:8000`. Base model requests pass through unchanged. LoRA adapter requests trigger loraplex to fetch and store the adapter files, then proxy to vLLM which loads them from the shared directory.

```bash
curl http://localhost:9090/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "your-org/your-lora-adapter",
    "messages": [{"role": "user", "content": "Hello"}],
    "max_tokens": 50
  }'
```

### Run (multi-node)

On each node, pass `--self` and `--peers`:

```bash
# Node 1
./bin/loraplex --listen :9090 --self 10.0.0.1:9090 \
  --vllm-url http://localhost:8000 \
  --peers 10.0.0.1:9090,10.0.0.2:9090,10.0.0.3:9090

# Node 2
./bin/loraplex --listen :9090 --self 10.0.0.2:9090 \
  --vllm-url http://localhost:8000 \
  --peers 10.0.0.1:9090,10.0.0.2:9090,10.0.0.3:9090
```

### Run (Kubernetes)

In K8s, loraplex discovers peers automatically via the Endpoints API so you don't need to list peers manually. See [`examples/k8s/`](examples/k8s/) for full manifests including RBAC, headless Service, and Deployment.

```bash
kubectl apply -f examples/k8s/manifests.yaml
```

Key requirements:

- A **headless Service** (`clusterIP: None`) selecting loraplex pods so the Endpoints API lists their IPs.
- A **ServiceAccount** with RBAC permission to `get` Endpoints in the namespace.
- Both the vLLM and loraplex containers must share the storage directory via an `emptyDir` volume.
- vLLM environment variables (`VLLM_ALLOW_RUNTIME_LORA_UPDATING`, `VLLM_PLUGINS`, `VLLM_LORA_RESOLVER_CACHE_DIR`) must be set on the vLLM container.

Use `--config` to load a YAML file instead of CLI flags. See [`config.example.yaml`](config.example.yaml) for all options. CLI flags override config file values.

## How It Works

loraplex and vLLM share a directory on disk. loraplex writes adapter files there, and vLLM's filesystem resolver reads from there. loraplex does not modify requests to vLLM or inject paths. It ensures the files exist before proxying, and vLLM's resolver independently discovers them.

```
loraplex writes ──▶ /mnt/nvme/loraplex/{adapter}/adapter_config.json
                    /mnt/nvme/loraplex/{adapter}/adapter_model.safetensors

vLLM reads    ◀── VLLM_LORA_RESOLVER_CACHE_DIR=/mnt/nvme/loraplex
```

vLLM manages adapters in GPU slots and CPU memory with its own LRU cache (`--max-loras`, `--max-cpu-loras`). loraplex manages the layer below that: **files on disk**. It ensures adapter files are present in the shared directory before proxying requests to vLLM, handles on-demand fetching from remote origins, bounds disk usage with LRU eviction, and routes requests to the node that already has the adapter stored.

```
Client request (model: "acme/summarizer-lora")
  │
  ▼
loraplex (:9090)
  │
  ├─ base model? ──▶ passthrough to vLLM (no storage needed)
  │
  ├─ consistent hash ──▶ this node owns it?
  │     │                    │
  │     │ no                 │ yes
  │     ▼                    ▼
  │   forward to        ensure adapter files exist in shared dir
  │   owner node          │
  │     │               on disk? ──▶ proxy to vLLM
  │     ▼                │
  │   (owner runs        │ not on disk
  │    same flow)        ▼
  │                   fetch from HuggingFace/S3
  │                   write to shared dir
  │                     │
  │                     ▼
  │                   proxy to vLLM
  │
  ▼
vLLM (:8000)
  filesystem resolver finds adapter in shared dir
  loads adapter weights into CPU/GPU memory
  runs inference
```

## API

loraplex is a transparent OpenAI-compatible proxy. All vLLM endpoints pass through.

### Proxy endpoints (passthrough to vLLM)

- `POST /v1/chat/completions`
- `POST /v1/completions`
- `GET /v1/models`
- Any other path is proxied as-is

### Admin endpoints

```bash
# Storage stats
curl http://localhost:9090/admin/cache/stats

# Pre-warm adapters
curl -X POST http://localhost:9090/admin/cache/warmup \
  -d '{"adapters": ["acme/summarizer-lora", "acme/translator-lora"]}'

# Evict an adapter (evicts locally and broadcasts to all peers)
curl -X POST http://localhost:9090/admin/cache/evict \
  -d '{"adapter": "acme/summarizer-lora"}'

# List peers
curl http://localhost:9090/admin/peers

# Health check (includes vLLM health, load, pending requests)
curl http://localhost:9090/healthz

# Prometheus metrics
curl http://localhost:9090/metrics
```

## Examples and Deployment Modes

loraplex is composable. Pick the mode that fits your stack:

**Standalone gateway.** loraplex handles storage and routing. Point a load balancer at your loraplex nodes.

**Storage-only sidecar.** Deploy loraplex 1:1 with each vLLM pod. Use an external router (AIBrix, gateway-api-inference-extension) for request distribution. Set `discovery.mode: static` with only `localhost` as a peer.

**Kubernetes.** Use `discovery.mode: k8s` with a headless Service. loraplex discovers peers automatically via the Endpoints API. Requires a ServiceAccount with RBAC to read Endpoints. See [`examples/k8s/`](examples/k8s/).

See the [`examples/`](examples/) directory for ready-to-use configs:

| Path                                                          | Use case                                                                  |
| ------------------------------------------------------------- | ------------------------------------------------------------------------- |
| [`single-node.yaml`](examples/single-node.yaml)               | One loraplex + one vLLM, HuggingFace origin                               |
| [`multi-node.yaml`](examples/multi-node.yaml)                 | 3-node cluster with static peer discovery                                 |
| [`cache-only-sidecar.yaml`](examples/cache-only-sidecar.yaml) | Storage sidecar behind an external router (AIBrix, etc.) with S3 origin   |
| [`k8s/`](examples/k8s/)                                       | Full K8s deployment: RBAC, headless Service, Deployment with vLLM sidecar |

## Architecture Details

### Storage

loraplex manages a single directory on disk. This is the directory vLLM's filesystem resolver reads from. Point it at your fastest available local storage — NVMe, tmpfs, or any local mount.

The `EnsureAdapter` call guarantees adapter files exist in this directory:

1. Already on disk? Nothing to do.
2. Not on disk? Download from origin, write to the directory.

| Config             | Default              | Description                                                                                   |
| ------------------ | -------------------- | --------------------------------------------------------------------------------------------- |
| `storage.dir`      | `/mnt/nvme/loraplex` | Path to the adapter storage directory. Must match `VLLM_LORA_RESOLVER_CACHE_DIR`.             |
| `storage.max_size` | `"100GB"`            | Maximum total size of stored adapters. When full, the least recently used adapter is evicted. |

vLLM also has its own CPU memory LRU cache (`--max-cpu-loras`) for loaded adapter weights. If vLLM still has the weights in CPU memory, it won't re-read from disk at all. The disk storage matters most when vLLM's CPU cache is also full and it needs to reload from the filesystem.

### Routing

Consistent hashing maps each adapter to a primary owner node. Under normal conditions, a 3-node cluster stores 3x the adapters of a single node, not 1x with redundant copies. When overflow protection kicks in, multiple nodes may store the same hot adapter (see below).

When a request arrives at the wrong node, it forwards to the owner. If the owner is down, the request is retried on the next peer in the ring. If both fail, the receiving node handles it locally (fetches the adapter itself). Forwarding adds a few milliseconds of overhead within a datacenter.

**Overload protection.** Each node reports its load (pending requests / max concurrent) via `/healthz`. Peers poll each other's health periodically. When a node's reported load exceeds `overflow_threshold` (default 0.8), other nodes stop forwarding to it and handle requests locally instead. If a peer is unreachable, its load is treated as 1.0 so no traffic is sent to it. This is bidirectional: if node B is overloaded with a hot adapter, nodes A and C notice via health polling and absorb the overflow by storing and serving that adapter locally. The `overflow_local_total` metric tracks how often this happens. Actual inference rate limiting is left to vLLM (`--max-num-seqs`, `--max-num-batched-tokens`).

**Hash key.** The `hash_on` config controls what gets hashed to determine the owner node:

| `hash_on`               | Hash key                                | Use case                                                                                                                  |
| ----------------------- | --------------------------------------- | ------------------------------------------------------------------------------------------------------------------------- |
| `model` (default)       | The `model` field from the request body | Route by adapter. Most common.                                                                                            |
| `tenant`                | The `X-Tenant-ID` header                | Full tenant-node affinity. All of a tenant's adapters land on the same node.                                              |
| `tenant/model`          | `{tenant}/{model}`                      | Tenant A and tenant B using the same adapter name route to different nodes. Falls back to model-only if no tenant header. |
| `header:X-Region`       | Value of the `X-Region` header          | Route by any custom header (region, customer ID, environment, etc.).                                                      |
| `header:X-Region/model` | `{header}/{model}`                      | Composite: route by header + adapter name. Falls back to model-only if header is missing.                                 |

**Peer discovery.** Three modes for how nodes find each other:

| Mode     | Config                                | How it works                                                                       |
| -------- | ------------------------------------- | ---------------------------------------------------------------------------------- |
| `static` | `--peers 10.0.0.1:9090,10.0.0.2:9090` | Fixed peer list. Use for bare metal or fixed-size clusters.                        |
| `k8s`    | `discovery.mode: k8s`                 | Polls K8s Endpoints API for a headless Service. Auto-discovers pods as they scale. |
| `file`   | `discovery.mode: file`                | Shared directory with heartbeat files. Use with NFS or EFS without K8s.            |

In K8s mode, loraplex polls the Endpoints API every 5 seconds. When pods scale up or down, the consistent hash ring rebuilds automatically. If a request is forwarded to a peer that went down between polls, it retries the next peer on the ring before falling back to local.

### Origins

loraplex fetches adapter files from one or more remote origins. If the first origin fails, it tries the next.

| Origin          | Content ID for invalidation     | Config                                            |
| --------------- | ------------------------------- | ------------------------------------------------- |
| **HuggingFace** | Git commit SHA of the repo      | `type: huggingface`, optional `token`             |
| **S3**          | ETag from `HeadObject`          | `type: s3`, `bucket`, `region`, optional `prefix` |
| **HTTP**        | ETag header from `HEAD` request | `type: http`, `base_url`                          |

S3 uses the standard AWS credential chain (environment variables, IAM role, `~/.aws/credentials`). Adapter files are expected at `s3://{bucket}/{prefix}/{adapter-id}/adapter_config.json` and `adapter_model.safetensors`.

### Invalidation

loraplex uses a stale-while-revalidate pattern. Stored adapters are served immediately, and staleness checks happen in the background without blocking requests.

1. Each stored adapter has a `.loraplex_meta` file that records the origin, content ID (ETag or commit SHA), fetch timestamp, and TTL.
2. On every access, if the TTL has expired, a background goroutine calls `Head` on the origin. This is a lightweight check (S3 `HeadObject`, HuggingFace API call, or HTTP `HEAD`) that returns the current content ID without downloading files.
3. If the content ID matches what's stored, the timestamp is refreshed. No re-download.
4. If the content ID has changed (adapter was retrained and re-uploaded), the old files are evicted and a fresh copy is fetched from origin.

The current request always gets the stored version. The next request after the background re-fetch completes gets the updated adapter. Set `invalidation.ttl` to control how often checks happen (default: `"1h"`). Set to `"0"` to disable.

## Reference

### CLI Flags

| Flag          | Default                 | Description                                                             |
| ------------- | ----------------------- | ----------------------------------------------------------------------- |
| `--version`   |                         | Print version and exit                                                  |
| `--config`    |                         | Path to YAML config file                                                |
| `--listen`    | `:8080`                 | Listen address                                                          |
| `--self`      |                         | Self address for ring identity (e.g. `10.0.0.1:9090`)                   |
| `--vllm-url`  | `http://localhost:8000` | vLLM backend URL                                                        |
| `--dir`       | `/mnt/nvme/loraplex`    | Adapter storage directory (should match `VLLM_LORA_RESOLVER_CACHE_DIR`) |
| `--discovery` | `static`                | Discovery mode (`static`, `file`, `k8s`)                                |
| `--peers`     |                         | Comma-separated peer list for static discovery                          |

### Config File

See [`config.example.yaml`](config.example.yaml) for a complete annotated example. CLI flags override config file values. Environment variables in config are expanded (`${HF_TOKEN}`).

**`storage`** configures adapter storage. `dir` is the directory loraplex writes adapter files to. It should match `VLLM_LORA_RESOLVER_CACHE_DIR` so vLLM's filesystem resolver can find them. Point it at your fastest local storage (NVMe, tmpfs, or any mount). `max_size` bounds total disk usage with LRU eviction. Sizes are strings like `"100GB"`, `"500MB"`, `"1TB"`.

**`origins`** defines where to fetch adapters. Multiple origins are tried in order until one succeeds. Each entry needs a `type` (`huggingface`, `s3`, or `http`). HuggingFace accepts an optional `token` for private repos. S3 requires `bucket` and `region`, with an optional `prefix`. HTTP takes a `base_url` where adapters are served at `{base_url}/{adapter-id}/adapter_config.json`.

**`invalidation`** controls background freshness checks. `ttl` sets how long before a stored adapter's content ID (ETag or commit SHA) is re-checked against the origin. The check is lightweight (HEAD request, not a full download) and non-blocking. Set to `"0"` to disable.

**`routing`** controls request distribution. `hash_on` determines the consistent hash key (default: `model`). Supports `model`, `tenant`, `tenant/model`, `header:<name>`, and `header:<name>/model` for custom or composite routing. `overflow_threshold` (0.0-1.0) controls when overloaded nodes shed work to other nodes. `fallback_to_base_model` serves the base model if an adapter can't be loaded.

**`discovery`** sets how peers find each other. `static` uses an explicit list. `k8s` watches a headless Service (requires RBAC for Endpoints). `file` uses a shared directory with heartbeat files.

### Prometheus Metrics

All metrics are prefixed with `loraplex_`.

| Metric                     | Type      | Description                                    |
| -------------------------- | --------- | ---------------------------------------------- |
| `requests_total`           | counter   | Total requests by tenant and source            |
| `request_duration_seconds` | histogram | Request latency by tenant and source           |
| `cache_hits_total`         | counter   | Adapter lookups by source (local, origin)      |
| `cache_evictions_total`    | counter   | Evictions from storage                         |
| `cache_size_bytes`         | gauge     | Current storage size                           |
| `cache_items`              | gauge     | Current adapter count in storage               |
| `peer_forwards_total`      | counter   | Requests forwarded to peers                    |
| `forward_duration_seconds` | histogram | Peer forwarding latency                        |
| `overflow_local_total`     | counter   | Requests handled locally due to owner overload |
| `peers_active`             | gauge     | Number of active peers in the ring             |
| `ring_rebuilds_total`      | counter   | Ring rebuild events                            |
| `vllm_healthy`             | gauge     | 1 if vLLM health check passes                  |

## Development

```bash
make test          # unit + e2e tests
make lint          # go vet
make build         # build binary to bin/
make build-linux   # cross-compile for linux/amd64
make docker        # build Docker image
```

## License

Apache 2.0
