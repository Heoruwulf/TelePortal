# Security & Compliance

When running TelePortal in production—especially for healthcare (HIPAA) or enterprise use cases—security is paramount. This guide outlines the current security posture and necessary configurations.

## 1. Network Security

### 1.1 SIP and RTP (Unencrypted UDP)

By default, standard telephony SIP and RTP traffic is **unencrypted**.

* **Signaling:** SIP messages (including caller ID and metadata) are sent in plaintext over UDP 5060.
* **Media:** RTP audio packets are sent unencrypted over UDP 10000-20000.

**Securing the Telecom Leg:**
If your use case requires encryption over the public internet (e.g., SIP-TLS and SRTP), TelePortal does not currently terminate TLS or SRTP natively.

* **Recommendation:** Place an enterprise Session Border Controller (SBC) or an open-source proxy like Kamailio/OpenSIPS in front of TelePortal. The SBC handles the TLS/SRTP certificates and passes unencrypted traffic to TelePortal over a secure, private VPC network.

### 1.2 WebSockets (TLS)

The WebSocket connection between TelePortal and your AI agent **must** be secured.

* TelePortal listens on standard HTTP (port 8080).
* **Recommendation:** Use a reverse proxy (like Nginx, Traefik, or an AWS Application Load Balancer) to terminate HTTPS/WSS (WebSocket Secure). Never expose the raw port 8080 to the public internet without TLS.

## 2. Authentication (JWT)

To prevent unauthorized agents from listening to or injecting audio into active calls, you must enable JWT authentication.

1. Generate a strong, random secret.
2. Set `TELEPORTAL_AUTH_JWT_SECRET=<your_secret>` in your environment.
3. When your AI agent connects, it must provide a valid JWT signed with that secret in the `Authorization: Bearer <token>` header.

*If this variable is left empty, the WebSocket API is completely open.*

## 3. Data Privacy (Recordings)

TelePortal includes a feature to record calls (SIP Rx and WebSocket Tx) to local disk as synchronized stereo WAV files.

* **Enabling/Disabling:** If `TELEPORTAL_AUDIO_RECORDING_PATH` is empty, no audio is written to disk.
* **Compliance:** If enabled, the resulting `.wav` files contain unencrypted, raw audio of the conversation. You are responsible for:
  * Securing access to the `recordings/` directory (or the mounted Docker volume).
  * Implementing a data retention policy to delete old recordings.
  * Ensuring compliance with local wiretapping and consent laws (e.g., playing a "This call may be recorded" prompt).

## 4. Denial of Service (DoS) Protection

TelePortal is designed to be highly resilient, but UDP services are susceptible to floods.

* **Max Calls:** Use the `TELEPORTAL_MAX_CALLS` environment variable. If set to `1000`, the 1001st concurrent incoming `INVITE` will be immediately rejected with a `486 Busy Here` response, preventing memory exhaustion.
* **Firewall:** Restrict inbound traffic on UDP 5060 to only the IP addresses of your SIP provider (e.g., Twilio's IP ranges). Do not leave port 5060 open to `0.0.0.0/0`.
