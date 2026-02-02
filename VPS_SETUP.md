# Deploying Helios on a VPS

This guide explains how to deploy the Helios server on a Virtual Private Server (VPS) and transfer files securely.

## Prerequisites

*   A Linux VPS (Ubuntu/Debian recommended)
*   SSH access to the VPS
*   Go installed (optional, if building on VPS)
*   Root or sudo privileges for network configuration

## 1. Build Helios for Linux

Since your local machine is likely macOS, you need to cross-compile Helios for the Linux VPS.

Run this command in the project root:

```bash
GOOS=linux GOARCH=amd64 go build -o helios-linux ./cmd/helios
```

This creates a `helios-linux` binary compatible with standard Linux servers.

## 2. Transfer Binary to VPS

Use `scp` to copy the binary to your server:

```bash
scp helios-linux user@<VPS_IP>:~/helios
```

*Replace `<VPS_IP>` with your server's IP address.*

## 3. Configure Firewall (Critical Step)

Helios uses **UDP port 4433** by default. QUIC runs over UDP, not TCP. You must open this port.

### If using UFW (Ubuntu default):

```bash
sudo ufw allow 4433/udp
```

### If using AWS / EC2:
1.  Go to **Security Groups**.
2.  Edit **Inbound Rules**.
3.  Add Rule:
    *   **Type**: Custom UDP
    *   **Port Range**: 4433
    *   **Source**: 0.0.0.0/0 (or your specific IP)

### If using standard iptables:

```bash
sudo iptables -A INPUT -p udp --dport 4433 -j ACCEPT
```

## 4. Run the Server

SSH into your VPS and start the server:

```bash
chmod +x helios
./helios serve
```

This will listen on `0.0.0.0:4433` by default.

### Advanced Usage (Running in background)

To keep the server running after you disconnect SSH, use `screen` or `tmux`:

```bash
# Start a new screen session
screen -S helios

# Run server
./helios serve --output /path/to/save/files --no-tui

# Detach by pressing Ctrl+A, then D
```

To re-attach later: `screen -r helios`

## 5. Connect from Client

On your local machine (macOS/Windows), use the `helios send` command:

```bash
./helios send ./my-large-file.zip --to <VPS_IP>:4433
```

Since the server uses a self-signed certificate, Helios is configured to skip verification by default for ease of use. If you changed config `insecure_skip_verify: false`, you will need to manage certificates manually.

## Troubleshooting

**Connection Timeout?**
*   Ensure you allowed **UDP** traffic, not TCP.
*   Check if the server is running (`netstat -ulpn | grep helios`).
*   Try `nc -z -u -v <VPS_IP> 4433` from your local machine to test connectivity.

**Permission Denied?**
*   Make sure you have write permissions for the output directory on the VPS.
