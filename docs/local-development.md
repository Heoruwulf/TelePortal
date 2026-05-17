# Local Development Guide

This guide helps contributors set up TelePortal locally for testing and development without needing a full production SIP provider.

## 1. Environment Setup

1. **Clone the repository:** `git clone ...`
2. **Copy the sample environment:** `cp sample.env .env`
3. **Local Networking:** For local testing, you don't need to set the `EXTERNAL_ADDRESS` variables. Ensure they are empty or set to `127.0.0.1`.

    ```env
    TELEPORTAL_SIP_BIND_ADDRESS=127.0.0.1
    TELEPORTAL_RTP_BIND_ADDRESS=127.0.0.1
    ```

## 2. Using the Loadtest Tool (TUI)

TelePortal includes a built-in load testing tool (`cmd/loadtest`) with a Terminal User Interface (TUI). This tool acts as both the SIP caller and the WebSocket AI agent, allowing you to test the full pipeline entirely locally.

### Running a Test

1. Start the main TelePortal service in one terminal:

    ```bash
    make run
    ```

2. Open a second terminal and build/run the load test tool:

    ```bash
    make build-loadtest
    ./build/loadtest -sip-addr "127.0.0.1:5060" -calls 10 -duration 30s
    ```

    This will simulate 10 concurrent callers talking to 10 AI agents for 30 seconds.

## 3. Simulating Real Calls (Linphone/MicroSIP)

If you want to manually test audio quality using a real microphone, you can use a free softphone application.

1. Download **MicroSIP** (Windows) or **Linphone** (Mac/Linux).
2. Configure a new SIP account in the softphone:
    * **SIP Server:** `127.0.0.1:5060`
    * **Username:** `test` (can be anything)
3. Ensure your softphone is set to use the **PCMU (G.711u)** or **PCMA (G.711a)** codec. Disable Opus, G.722, etc.
4. Dial any number (e.g., `1234`). TelePortal will answer immediately.

## 4. Testing the WebSocket Client

You can write a simple Go script using `pkg/client` to connect to the active call and stream audio to your speaker or record it to a file. See the examples in `pkg/client/client_test.go` for inspiration.

## 5. QA Requirements

Before submitting a Pull Request, you must ensure your code passes all quality checks:

```bash
make qa
```

This runs:

* `go fmt`: Standard Go formatting.
* `go vet`: Standard Go static analysis.
* `fieldalignment`: Ensures structs are memory-optimized (run `go install golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@latest` if missing).
* `staticcheck`: Advanced static analysis (run `go install honnef.co/go/tools/cmd/staticcheck@latest` if missing).
