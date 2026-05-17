# DevOps Guide: Deploying TelePortal

TelePortal is a high-performance, bi-directional audio bridge written in Go. It connects traditional VoIP systems (via SIP/RTP) to modern AI services (via WebSockets).

This guide outlines the infrastructure requirements, configuration details, and deployment strategies for running TelePortal in production on either a standalone cloud instance or a Kubernetes cluster.

## 1. System Requirements

TelePortal is designed to be highly efficient with a deterministic memory footprint (~660 KB per active call).

* **CPU:** 2 vCPU minimum (scales efficiently with more cores).
* **Memory:** 1 GB minimum (can handle ~1,000 concurrent calls).
* **Network:** High bandwidth, low-latency connection. **Critically, the server must be able to receive UDP traffic on a large port range (for RTP).**
* **Dependencies:**
  * **Redis:** Required for state management and webhook notifications. Can be deployed alongside or as a managed service (e.g., AWS ElastiCache, GCP Memorystore).

## 2. Network & Port Configuration

Deploying TelePortal requires careful network configuration because it acts as a media gateway.

### Exposed Ports

| Port Range | Protocol | Purpose |
| :--- | :--- | :--- |
| `5060` | UDP/TCP | SIP Signaling. Used to receive incoming call setups. |
| `8080` | TCP | HTTP/WebSocket API. Used for AI agent connections, health checks, and metrics. |
| `10000-20000` | UDP | RTP Media Streams. Dynamically allocated per call (two ports per call: RTP and RTCP). *This range is configurable.* |

### NAT and External IP Configuration

If TelePortal is deployed behind a NAT (which is typical in cloud environments like AWS, GCP, or Kubernetes), **you must explicitly configure its external IP address**. If the external IP is not provided, the internal IP will be advertised in SIP and SDP payloads, resulting in one-way or no-way audio.

Set the following environment variables:

* `TELEPORTAL_SIP_EXTERNAL_ADDRESS`: The public IP for SIP signaling.
* `TELEPORTAL_RTP_EXTERNAL_ADDRESS`: The public IP for RTP media.

## 3. Configuration (Environment Variables)

TelePortal is configured entirely via environment variables (12-factor app methodology).

### Essential Production Variables

| Variable | Description |
| :--- | :--- |
| `TELEPORTAL_SIP_EXTERNAL_ADDRESS` | **[REQUIRED]** Public IP to advertise in SIP/SDP. |
| `TELEPORTAL_RTP_EXTERNAL_ADDRESS` | **[REQUIRED]** Public IP to advertise in SDP for media streams. |
| `TELEPORTAL_WS_CODEC` | **[REQUIRED]** Audio codec for WebSockets (`L16`, `PCMU`, `PCMA`, `PASS`). `PASS` is recommended for performance. |
| `TELEPORTAL_HTTP_PUBLIC_URL` | **[REQUIRED]** The public URL of this instance (used for Webhook payloads). e.g., `https://teleportal.example.com` |
| `TELEPORTAL_REDIS_ADDRESS` | **[REQUIRED]** Address of the Redis instance. e.g., `redis:6379` |
| `TELEPORTAL_AUTH_JWT_SECRET` | **[REQUIRED]** Secret key for securing WebSocket connections. |
| `TELEPORTAL_LOG_FORMAT` | Set to `json` for structured logging in production. |
| `TELEPORTAL_FEATURES_PROMETHEUS` | Set to `true` to expose the `/metrics` endpoint. |
| `TELEPORTAL_MAX_CALLS` | Set to limit concurrent calls (e.g., `1000`). Responds with 486 Busy if exceeded. |

*See `sample.env` in the repository root for the exhaustive list of all configuration options.*

## 4. Application Lifecycle and Signals

TelePortal supports advanced signal handling for zero-downtime operations and debugging.

### Graceful Shutdown (`SIGINT` / `SIGTERM`)

When Kubernetes or a system service manager stops the process, TelePortal enters a "draining" state:

1. It stops accepting new SIP INVITEs (responding with 503 Service Unavailable).
2. It waits for all currently active calls to hang up naturally.
3. If the calls do not end before the `TELEPORTAL_SHUTDOWN_TIMEOUT` (default: 5m), it forcefully terminates them and exits.

### Configuration Hot-Reload (`SIGHUP`)

You can reload specific configurations without dropping active calls or restarting the network listeners by sending a `SIGHUP` signal to the process.

**Reloadable parameters:**

* `TELEPORTAL_LOG_LEVEL` (e.g., dynamically switch from `info` to `debug`)
* `TELEPORTAL_MAX_CALLS`
* `TELEPORTAL_AUDIO_JB_MIN_PACKETS`

### Diagnostic Dump (`SIGQUIT`)

Sending a `SIGQUIT` signal will force TelePortal to dump the current stack trace of all goroutines to `stderr` before initiating a graceful shutdown. This is vital for diagnosing deadlocks or performance bottlenecks in a running production instance before it is killed.

## 5. Observability

* **Health Checks:** TelePortal exposes standard Kubernetes-style health endpoints on the HTTP port (`8080`):
  * `GET /healthz` (or `/livez`): Liveness probe. Returns `200 OK` as long as the process is running.
  * `GET /readyz`: Readiness probe. Returns `200 OK` when the service is fully initialized. During a graceful shutdown (draining), it returns a `503 Service Unavailable`.
*   **Metrics:** If `TELEPORTAL_FEATURES_PROMETHEUS=true`, Prometheus metrics are exposed at `GET /metrics` on the HTTP port (`8080`). This includes active call counts, jitter buffer stats, and memory usage.


* **Logging:** Configure `TELEPORTAL_LOG_FORMAT=json` for compatibility with log aggregators (ELK, Datadog, CloudWatch).

---

## 6. Deployment Scenarios

### Scenario A: Docker Compose (Single VM / Standalone)

This is the simplest way to run TelePortal on a single cloud VM (e.g., AWS EC2, DigitalOcean Droplet). It uses `network_mode: "host"` to easily manage the large UDP port range required for RTP.

**Prerequisites:**

1. A Linux VM with a public IP.
2. Docker and Docker Compose installed.
3. Firewall configured to allow TCP `8080`, UDP `5060`, and UDP `10000-20000`.

**Steps:**

1. Clone the repository.
2. Copy `sample.env` to `.env`.
3. Update the `.env` file, critically setting:
    * `TELEPORTAL_SIP_EXTERNAL_ADDRESS=<YOUR_VM_PUBLIC_IP>`
    * `TELEPORTAL_RTP_EXTERNAL_ADDRESS=<YOUR_VM_PUBLIC_IP>`
4. Start the service:

    ```bash
    docker-compose up -d teleportal redis
    ```

### Scenario B: Kubernetes Deployment

Deploying TelePortal in Kubernetes is more complex due to the RTP UDP port range.

**The Ingress Challenge:** Standard Kubernetes Ingress controllers (like Nginx) operate at Layer 7 (HTTP) and cannot handle Layer 4 UDP traffic or large port ranges efficiently.

**Recommended Strategies:**

1. **HostNetwork (Easiest, Less Flexible):**
    Run the TelePortal Pod with `hostNetwork: true`. This bypasses the Kubernetes network proxy, binding directly to the Node's interface.
    * *Pros:* Simplifies RTP port management. High performance.
    * *Cons:* You can only run one TelePortal pod per Node to avoid port conflicts. You must expose the Node's public IP as the `EXTERNAL_ADDRESS`.

2. **LoadBalancer Service (Recommended):**
    Use a Kubernetes `Service` of type `LoadBalancer`.
    * *Pros:* Clean integration with Cloud providers.
    * *Cons:* Most cloud load balancers do not support forwarding a range of 10,000 UDP ports efficiently. You may need to severely restrict the `TELEPORTAL_RTP_PORT_MIN` and `MAX` range (e.g., to 100 ports) and accept fewer concurrent calls per instance, or use a specialized Layer 4 load balancer.

3. **Dedicated Node Pool with Public IPs:**
    Assign public IPs directly to the worker nodes running TelePortal pods. Use a NodePort service or HostNetwork to route traffic.

**Example Kubernetes Configuration Snippets:**

**Deployment (HostNetwork approach):**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: teleportal
spec:
  replicas: 2 # Assuming you have at least 2 nodes
  selector:
    matchLabels:
      app: teleportal
  template:
    metadata:
      labels:
        app: teleportal
    spec:
      hostNetwork: true # Bind directly to Node IP
      dnsPolicy: ClusterFirstWithHostNet
      containers:
        - name: teleportal
          image: your-registry/teleportal:latest
          env:
            # You must dynamically pass the Node's external IP
            - name: TELEPORTAL_SIP_EXTERNAL_ADDRESS
              valueFrom:
                fieldRef:
                  fieldPath: status.hostIP # Or a mechanism to get the public IP
            - name: TELEPORTAL_RTP_EXTERNAL_ADDRESS
              valueFrom:
                fieldRef:
                  fieldPath: status.hostIP
            - name: TELEPORTAL_REDIS_ADDRESS
              value: "redis-master.default.svc.cluster.local:6379"
            - name: TELEPORTAL_FEATURES_PROMETHEUS
              value: "true"
          ports:
            - containerPort: 8080
              name: http
            - containerPort: 5060
              name: sip
              protocol: UDP
          readinessProbe:
            httpGet:
              path: /readyz
              port: 8080
            initialDelaySeconds: 5
            periodSeconds: 10
```

**Reloading Config in Kubernetes:**

If you update a ConfigMap containing your environment variables, you can trigger a hot-reload without dropping calls by executing the `SIGHUP` signal inside the pod:

```bash
kubectl exec <teleportal-pod-name> -- kill -SIGHUP 1
```

*(Note: Process ID 1 assumes TelePortal is the main process in the container).*
