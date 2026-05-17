# Performance Tuning Guide

TelePortal is designed for extreme concurrency, but to safely handle thousands of simultaneous active calls, the host OS and application configuration must be tuned.

## 1. Operating System Tuning (Linux)

### 1.1 File Descriptors (`ulimit`)

Every active call consumes multiple file descriptors (1 UDP socket for SIP, 2 UDP sockets for RTP/RTCP, 1 TCP socket for the WebSocket).

**Requirement:** For 1,000 calls, you need at least 4,000 file descriptors.
**Fix:** Increase the `ulimit` for the user running TelePortal.

```bash
ulimit -n 65535
```

*(In production, set this permanently via `/etc/security/limits.conf` or your `systemd` service file using `LimitNOFILE=65535`).*

### 1.2 UDP Buffer Sizes (`sysctl`)

High-volume UDP traffic (RTP) can overwhelm the kernel's default receive buffers, leading to dropped audio packets before TelePortal even sees them.

**Fix:** Increase the max and default OS buffer sizes.
Add to `/etc/sysctl.conf`:

```ini
net.core.rmem_max=26214400
net.core.rmem_default=26214400
net.core.wmem_max=26214400
net.core.wmem_default=26214400
```

Apply with `sudo sysctl -p`.

---

## 2. TelePortal Configuration Tuning

### 2.1 The `TELEPORTAL_WS_CODEC`

The choice of WebSocket codec heavily impacts CPU usage.

* `PASS`: The highest performance mode. No transcoding occurs. Data is passed directly from RTP to the WebSocket. Recommended for high-density deployments.
* `L16`: Moderate CPU impact. TelePortal must decompress G.711 to PCM.
* `PCMU`/`PCMA`: Moderate CPU impact if the SIP leg negotiated something else, otherwise it acts like `PASS`.

### 2.2 Jitter Buffer Sizing

The `TELEPORTAL_AUDIO_JB_MIN_PACKETS` controls how long TelePortal waits to reconstruct audio before sending it to the AI agent.

* **Default:** `50` packets (~1 second at 20ms ptime).
* **Low Latency (Fast networks):** Decrease to `10` or `20` to reduce agent response time. If the audio becomes choppy, increase it.
* **High Latency (Poor networks):** Increase to `100` if callers are on bad cellular connections, at the cost of slower AI response times.
* **Hot-Reloadable:** You can adjust this value and send `SIGHUP` to apply it without dropping calls.

---

## 3. Profiling (`pprof`)

If you suspect a performance bottleneck, you can enable Go's built-in profiler.

1. Set `TELEPORTAL_FEATURES_PPROF=true`.
2. While the system is under load, capture a 30-second CPU profile:

    ```bash
    go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30
    ```

3. Capture a Heap (Memory) profile:

    ```bash
    go tool pprof http://localhost:6060/debug/pprof/heap
    ```
