# Troubleshooting & Diagnostics

This guide covers common issues when operating TelePortal and how to diagnose them using built-in tools.

## 1. Common Issues

### 1.1 One-Way Audio (or No Audio)

This is the most common issue in VoIP, almost always caused by NAT or Firewall misconfiguration.

* **Symptom:** The AI agent receives audio (caller speaks, AI transcribes), but the caller hears silence when the AI speaks.
* **Cause:** TelePortal is receiving RTP packets from the provider, but the provider is dropping the RTP packets TelePortal sends back.
* **Fix:**
    1. Verify `TELEPORTAL_RTP_EXTERNAL_ADDRESS` is set to your server's **Public IP**.
    2. Verify your cloud firewall allows inbound UDP traffic on ports `10000-20000`.

### 1.2 Calls Drop After Exactly 30 Seconds

* **Symptom:** The call connects, audio flows perfectly, but drops abruptly after ~30 seconds.
* **Cause:** The SIP provider did not receive the `ACK` message from TelePortal, or TelePortal sent the `ACK` to an unreachable private IP due to NAT rewriting issues.
* **Fix:** Verify `TELEPORTAL_SIP_EXTERNAL_ADDRESS` is set. This ensures TelePortal puts its public IP in the `Contact` header, allowing the provider to route subsequent requests correctly.

### 1.3 `488 Not Acceptable Here`

* **Symptom:** Call fails immediately. TelePortal logs show a 488 response.
* **Cause:** Codec mismatch. The SIP provider offered codecs that TelePortal doesn't support (e.g., forcing G.729 or Opus).
* **Fix:** Configure your SIP trunk to allow `G.711 PCMU` (mu-law) or `G.711 PCMA` (a-law).

---

## 2. Diagnostic Tools

### 2.1 Dynamic Log Levels (`SIGHUP`)

If you encounter an issue in production but don't want to restart the service (which would drop active calls), you can dynamically change the log level to `debug`.

1. Update your environment configuration to `TELEPORTAL_LOG_LEVEL=debug`.
2. Send a `SIGHUP` to the TelePortal process:

    ```bash
    kill -SIGHUP $(pidof teleportal)
    ```

    *(Or `kubectl exec <pod> -- kill -SIGHUP 1` in Kubernetes).*

### 2.2 Goroutine Stack Dumps (`SIGQUIT`)

If the application appears frozen, is leaking memory, or failing to shutdown gracefully, you can force it to dump its state.

```bash
kill -SIGQUIT $(pidof teleportal)
```

This will print a complete trace of all running goroutines to `stderr` and then gracefully terminate the process. Look for goroutines stuck in `chan receive` or `semacquire`.

### 2.3 Prometheus Metrics

Enable metrics (`TELEPORTAL_FEATURES_PROMETHEUS=true`) and check `http://<ip>:8080/metrics`.

* Monitor `teleportal_jitter_buffer_underruns_total`. A high underrun count indicates the network is delivering packets too slowly or out of order.

### 2.4 PCAP Generation (Packet Capture)

Sometimes you need to see the raw packets. Install `tcpdump` on your host or within the container.

**Capture SIP Signaling (Port 5060):**

```bash
tcpdump -i any -n -s 0 port 5060 -w sip_traffic.pcap
```

**Capture SIP and RTP (All traffic to the server):**

```bash
tcpdump -i any -n -s 0 udp -w all_voip_traffic.pcap
```

You can open these `.pcap` files in Wireshark. Use Wireshark's "Telephony -> VoIP Calls" feature to visually analyze the call flow and play back the raw RTP audio.
