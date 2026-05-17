# SIP Provider Integration Guide

TelePortal relies on a SIP Trunk to connect to the traditional telephony network. This guide outlines how to configure TelePortal and common SIP providers to work together.

## General Principles

1. **Symmetric RTP:** TelePortal automatically supports symmetric RTP. It will send media back to the IP and port from which it receives media. This helps bypass NAT issues on the provider side.
2. **Public IP Requirement:** Because TelePortal acts as a media gateway, you **must** configure `TELEPORTAL_SIP_EXTERNAL_ADDRESS` and `TELEPORTAL_RTP_EXTERNAL_ADDRESS` if TelePortal is behind a NAT (e.g., in AWS/GCP or a home network).
3. **Codec Negotiation:** TelePortal natively supports `G.711 PCMU` (mu-law) and `G.711 PCMA` (a-law). Ensure your provider is configured to offer one of these codecs. `L16` negotiation via SIP SDP is also supported but rarely offered by public providers.

---

## Twilio Elastic SIP Trunking

Twilio is one of the most common providers for routing numbers to a SIP infrastructure.

### Twilio Configuration

1. **Create a SIP Trunk:** In the Twilio Console, navigate to Voice -> Elastic SIP Trunking.
2. **Origination URI:** This tells Twilio where to send incoming calls.
    * Set the URI to: `sip:teleportal@<YOUR_TELEPORTAL_PUBLIC_IP>:5060`
    * *(Note: The username "teleportal" is ignored by the system, but Twilio requires a user part).*
3. **Secure Trunking:** If you require TLS/SRTP, note that TelePortal currently supports unencrypted UDP signaling and RTP by default. You may need to place a proxy like Kamailio or an SBC in front of TelePortal for TLS termination, or disable Secure Trunking in Twilio.
4. **Access Control Lists (ACL):** Ensure Twilio's IP addresses are allowed through your cloud firewall (Security Group) on UDP port 5060 and UDP ports 10000-20000.

### TelePortal Configuration

No special configuration is needed beyond the standard external IPs.

```env
TELEPORTAL_SIP_EXTERNAL_ADDRESS=203.0.113.50
TELEPORTAL_RTP_EXTERNAL_ADDRESS=203.0.113.50
TELEPORTAL_WS_CODEC=PASS
```

---

## AWS Chime Voice Connector

AWS Chime provides a highly scalable SIP trunking service.

### AWS Chime Configuration

1. **Create a Voice Connector:** In the AWS Chime Console, create a new Voice Connector.
2. **Termination:** To allow TelePortal to receive calls, configure the Termination settings.
    * Add your TelePortal's public IP to the **Allowed IP addresses**.
3. **Origination:** If you plan to have TelePortal dial *out* (future feature support), configure the Origination settings with your AWS IP ranges.

### TelePortal Configuration

Similar to Twilio, ensure your external IPs are set.

---

## FreePBX / Asterisk (Local PBX)

If you are running TelePortal alongside a local PBX for testing or internal routing.

### FreePBX Configuration (PJSIP Trunk)

1. **Trunk Name:** `teleportal`
2. **Dialed Number Manipulation:** Configure as needed to route specific extensions to TelePortal.
3. **pjsip Settings -> General:**
    * `Authentication`: None
    * `Registration`: None
    * `SIP Server`: `<TELEPORTAL_INTERNAL_IP>`
    * `SIP Server Port`: `5060`
4. **pjsip Settings -> Advanced:**
    * `Rewrite Contact`: Yes (Helpful if FreePBX is also behind NAT).

### TelePortal Configuration

If TelePortal and FreePBX are on the same local network (e.g., 192.168.1.x), you **do not** need to set the external addresses. TelePortal will automatically use its bound local interface.

```env
TELEPORTAL_SIP_BIND_ADDRESS=0.0.0.0
TELEPORTAL_RTP_BIND_ADDRESS=0.0.0.0
# Leave EXTERNAL addresses empty for purely local traffic
TELEPORTAL_SIP_EXTERNAL_ADDRESS=
TELEPORTAL_RTP_EXTERNAL_ADDRESS=
```

---

## Common Issues

* **One-Way Audio (AI hears caller, Caller hears nothing):** The provider cannot reach TelePortal's RTP ports. Check your firewall rules (UDP 10000-20000) and ensure `TELEPORTAL_RTP_EXTERNAL_ADDRESS` is correctly set.
* **Call Drops Immediately (488 Not Acceptable Here):** The provider is forcing a codec that TelePortal does not support (e.g., G.729 or Opus). Force the provider to use G.711 (PCMU/PCMA).
* **SIP Timeout (No 100 Trying):** The provider cannot reach TelePortal on port 5060. Check firewall rules and `TELEPORTAL_SIP_EXTERNAL_ADDRESS`.
