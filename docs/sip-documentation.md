# TelePortal: SIP & RTP Reference for Telephony Engineers

TelePortal is a high-performance, bi-directional audio bridge designed to connect traditional VoIP infrastructure (via SIP/RTP) directly to modern AI applications and web services (via WebSockets).

This document serves as a comprehensive reference for telephony engineers integrating their PBX systems, SBCs, or SIP trunks with TelePortal.

---

## 1. Core Capabilities

TelePortal operates as a **User Agent Server (UAS)** terminating calls. It does not initiate outbound calls or act as a SIP Proxy (B2BUA).

### Supported SIP Methods

* **`INVITE`**: Initiates a new call session. TelePortal will negotiate media capabilities and attempt to establish the RTP stream.
* **`ACK`**: Acknowledges the final response to an `INVITE`.
* **`BYE`**: Terminates an active call. TelePortal supports both receiving `BYE` from the upstream system and initiating `BYE` when the connected AI agent closes the WebSocket connection.
* **`CANCEL`**: Cancels a pending `INVITE` before it is answered.
* **`OPTIONS`**: Used for SIP keep-alives and ping monitoring. TelePortal immediately responds with a `200 OK`.

### Media & Codec Support

TelePortal prioritizes high-performance, low-latency audio passing.

* **Supported Codecs (in order of preference):**
    1. `L16` (16-bit Linear PCM, 8000Hz or 16000Hz) - *Preferred for highest quality AI interactions.*
    2. `PCMU` (G.711 mu-law, 8000Hz) - *Standard for North American telephony.*
    3. `PCMA` (G.711 A-law, 8000Hz) - *Standard for International telephony.*
* **DTMF (RFC 2833 / RFC 4733):**
  * TelePortal fully supports out-of-band DTMF events (payload type typically 101).
  * In-band audio DTMF is passed through directly to the AI agent but is not parsed by TelePortal itself.
* **Packetization Time (ptime):**
  * Defaults to `20ms`.
  * Respects the `ptime` value provided in the incoming SDP offer.

### NAT Traversal and Networking

Because TelePortal bridges internal systems to external ones, correct IP advertisement is critical.

* **Strict Symmetric RTP:** TelePortal enforces symmetric RTP. It will send outbound RTP packets back to the exact IP address and UDP port from which it received the first inbound RTP packet, regardless of the address specified in the incoming SDP offer. This is crucial for navigating strict firewalls and NATs.
* **External Advertisement:** For deployments behind NAT, the `TELEPORTAL_SIP_EXTERNAL_ADDRESS` and `TELEPORTAL_RTP_EXTERNAL_ADDRESS` variables MUST be set. These dictate the IP addresses written into the SIP headers and the SDP body.

---

## 2. Call Lifecycle and Concurrency

### Connection Flow

1. **Incoming `INVITE`:** TelePortal receives the `INVITE` and validates readiness and capacity.
2. **SDP Answer:** If accepted, TelePortal negotiates the codec, allocates a local UDP port for RTP from its configured range (`TELEPORTAL_RTP_PORT_MIN` to `TELEPORTAL_RTP_PORT_MAX`), and returns a `200 OK` with its SDP Answer.
3. **Media Flow:** TelePortal begins listening for RTP. It acts as a jitter buffer, converting incoming RTP into a steady WebSocket stream for the AI agent.
4. **AI Connection:** The AI agent connects to the specific call via WebSocket.
5. **Termination:** The call ends when either the SIP side sends a `BYE`, or the AI agent disconnects the WebSocket (which prompts TelePortal to send a `BYE` to the SIP side).

### Capacity Management

TelePortal is designed to fail gracefully under load.

* **`TELEPORTAL_MAX_CALLS`**: This configuration dictates the absolute maximum number of concurrent active calls the instance will handle.
* **Load Balancing**: We strongly recommend deploying TelePortal instances behind a SIP-aware load balancer (like Kamailio or OpenSIPS) that can distribute calls based on instance health and capacity.

---

## 3. SIP Response Codes

TelePortal adheres strictly to RFC 3261. Below are the specific response codes TelePortal returns and the exact technical conditions that trigger them.

### Success Responses

* **`200 OK`**
  * **Context:** Returned in response to a successful `INVITE` or `OPTIONS` ping.
  * **Meaning:** The call has been accepted, media ports are allocated, and the SDP Answer is attached.

### Error Responses

Understanding these error codes is essential for debugging SIP trunk configurations.

* **`400 Bad Request`**
  * **Trigger:** The incoming `INVITE` contains a malformed SDP body that the Pion SDP parser cannot decode.
  * **Resolution:** Verify that your SBC or PBX is sending strictly compliant RFC 4566 SDP formats.
* **`486 Busy Here`**
  * **Trigger:** The number of active calls on this TelePortal instance has reached the limit defined by `TELEPORTAL_MAX_CALLS`.
  * **Resolution:** This is a signal for the upstream load balancer to attempt routing the call to a different instance or for the PBX to play an "all circuits busy" message.
* **`488 Not Acceptable Here`**
  * **Trigger:** TelePortal and the calling party could not agree on a common audio codec.
  * **Resolution:** Ensure that the upstream system is offering at least one of `L16`, `PCMU`, or `PCMA`. Note that TelePortal will explicitly reject offers that do not contain compatible codecs or if the WebSocket backend is configured for a specific codec that the caller does not support.
* **`503 Service Unavailable`**
  * **Trigger:** The TelePortal instance is starting up, shutting down, or has failed its internal readiness checks (`/readyz`).
  * **Resolution:** The upstream routing logic should mark this node as temporarily offline and route traffic elsewhere.
