# Deployment Guide (quicssh-punch)

This guide covers deploying `quicssh-punch` on Linux, OpenWrt, and Docker.
`quicssh-punch` is a UDP hole puncher/relay used with `quicssh`.

## 1. Prerequisites

- A rendezvous topic shared by both sides (example: `punch-<random>`).
- One side in **server mode** (`-target` points to local `quicssh` server).
- One side in **client mode** (no `-target`).
- Same UDP port on both sides (example: `44433`).
- Outbound UDP allowed to STUN and ntfy.sh.

Example topic:

```sh
TOPIC="punch-$(openssl rand -hex 8)"
echo "$TOPIC"
```

## 2. Build

### Linux (local machine)

```sh
git clone https://github.com/shafiev/quicssh-punch.git
cd quicssh-punch
go build -o quicssh-punch .
```

### Cross-build for OpenWrt (aarch64 example)

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
  go build -trimpath -ldflags "-s -w" -o quicssh-punch-linux-arm64 .
```

Other common targets:

```sh
# OpenWrt x86_64
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o quicssh-punch-linux-amd64 .

# OpenWrt arm (32-bit)
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -o quicssh-punch-linux-armv7 .

# macOS arm64
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o quicssh-punch-macos-arm64 .
```

## 3. Linux deployment (systemd)

### Server side (router/public host with quicssh server)

Install binary:

```sh
sudo cp quicssh-punch /usr/local/bin/quicssh-punch
sudo chmod 755 /usr/local/bin/quicssh-punch
```

Create `/etc/systemd/system/quicssh-punch.service`:

```ini
[Unit]
Description=quicssh UDP hole-punch relay (ntfy rendezvous)
After=network-online.target
Wants=network-online.target

[Service]
User=root
ExecStart=/usr/local/bin/quicssh-punch -port 44433 -role A -topic punch-XXXX -target 10.144.1.1:4433
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

Enable and start:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now quicssh-punch
sudo systemctl status quicssh-punch
```

### Client side (laptop/remote machine)

```sh
sudo cp quicssh-punch /usr/local/bin/quicssh-punch
sudo chmod 755 /usr/local/bin/quicssh-punch
/usr/local/bin/quicssh-punch -port 44433 -role B -topic punch-XXXX
```

Or as a user service/systemd unit with `-role B` and no `-target`.

### SSH config example

```sshconfig
Host openwrt-punch
    HostName 10.144.1.1
    User root
    IdentityFile ~/.ssh/id_rsa
    IdentitiesOnly yes
    BatchMode yes
    ConnectTimeout 30
    ProxyCommand /usr/local/bin/quicssh client --addr 127.0.0.1:44433
```

Connect:

```sh
ssh openwrt-punch
```

## 4. OpenWrt deployment

### Option A: Manual binary

1. Build or download the correct architecture binary.
2. Copy it to the router:

```sh
scp quicssh-punch-linux-arm64 root@ROUTER:/tmp/quicssh-punch.new
```

If `scp`/`sftp` is unavailable, use SSH stdin:

```sh
ssh root@ROUTER 'cat > /tmp/quicssh-punch.new' < quicssh-punch-linux-arm64
```

3. Install and restart:

```sh
ssh root@ROUTER '
cp /usr/bin/quicssh-punch /tmp/quicssh-punch.old
/etc/init.d/quicssh-punch stop
cp /tmp/quicssh-punch.new /usr/bin/quicssh-punch
chmod 755 /usr/bin/quicssh-punch
/etc/init.d/quicssh-punch start
/etc/init.d/quicssh-punch status
'
```

### Option B: OpenWrt package

Use `packaging/openwrt/` in a buildroot or package feed.

```sh
./scripts/feeds update -a
./scripts/feeds install -a
make menuconfig   # Network -> quicssh-punch
make package/quicssh-punch/compile V=s
```

Install the resulting `.ipk`, then configure:

```sh
uci set quicssh-punch.main.enabled='1'
uci set quicssh-punch.main.role='A'
uci set quicssh-punch.main.topic='punch-<random>'
uci set quicssh-punch.main.target='10.144.1.1:4433'
uci commit quicssh-punch
/etc/init.d/quicssh-punch enable
/etc/init.d/quicssh-punch start
```

For client side, leave `target` empty and set `role 'B'`.

## 5. Docker deployment

Build or use the published image:

```sh
docker run -d --network host ghcr.io/shafiev/quicssh-punch:latest \
  -port 44433 -role B -topic punch-XXXX
```

Use `--network host` on Linux so the punch uses the host NAT mapping.

Server side in Docker:

```sh
docker run -d --network host ghcr.io/shafiev/quicssh-punch:latest \
  -port 44433 -role A -topic punch-XXXX -target 10.144.1.1:4433
```

## 6. Useful flags

- `-port`: fixed UDP port (required).
- `-role`: role name, e.g. `A` or `B` (required).
- `-topic`: ntfy.sh rendezvous topic.
- `-target`: local `host:port` to relay to (server mode).
- `-peer`: static peer address (testing, skips rendezvous).
- `-stun` / `-stun2`: STUN servers for endpoint discovery and NAT detection.
- `-relay`: fallback relay address when direct punch stalls.
- `-secret`: shared secret for rendezvous auth and punch obfuscation.
- `-metrics`: HTTP listen address for health/metrics JSON.

Example with secret and metrics:

```sh
quicssh-punch -port 44433 -role A -topic punch-XXXX \
  -target 10.144.1.1:4433 -secret 'shared-secret' -metrics 127.0.0.1:9090
```

## 6a. macOS deployment (launchd)

Build for macOS:

```sh
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o quicssh-punch-macos-arm64 .
# or for Intel Macs:
GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o quicssh-punch-macos-amd64 .
```

Install binary:

```sh
sudo cp quicssh-punch-macos-arm64 /usr/local/bin/quicssh-punch
sudo chmod 755 /usr/local/bin/quicssh-punch
```

Create `/Library/LaunchDaemons/com.shafiev.quicssh-punch.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.shafiev.quicssh-punch</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/quicssh-punch</string>
        <string>-port</string>
        <string>44433</string>
        <string>-role</string>
        <string>B</string>
        <string>-topic</string>
        <string>punch-XXXX</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/var/log/quicssh-punch.log</string>
    <key>StandardErrorPath</key>
    <string>/var/log/quicssh-punch.err</string>
</dict>
</plist>
```

Set permissions and load:

```sh
sudo chown root:wheel /Library/LaunchDaemons/com.shafiev.quicssh-punch.plist
sudo chmod 644 /Library/LaunchDaemons/com.shafiev.quicssh-punch.plist
sudo launchctl bootstrap system /Library/LaunchDaemons/com.shafiev.quicssh-punch.plist
sudo launchctl kickstart -k system/com.shafiev.quicssh-punch
```

Server side uses the same plist but with `-role A -target 10.144.1.1:4433`.

## 6b. Windows deployment

Build for Windows:

```powershell
$env:GOOS="windows"
$env:GOARCH="amd64"
go build -trimpath -ldflags "-s -w" -o quicssh-punch.exe .
```

`quicssh-punch` is a normal foreground program, so on Windows it is easiest to run it as a service with **NSSM** (Non-Sucking Service Manager).

Install NSSM (example with Chocolatey):

```powershell
choco install nssm -y
```

Install and start the service:

```powershell
nssm install quicssh-punch C:\tools\quicssh-punch.exe
nssm set quicssh-punch AppDirectory C:\tools
nssm set quicssh-punch DisplayName "quicssh-punch"
nssm set quicssh-punch Description "quicssh UDP hole-punch relay"
nssm set quicssh-punch Start SERVICE_AUTO_START
nssm set quicssh-punch AppParameters "-port 44433 -role B -topic punch-XXXX"
nssm start quicssh-punch
```

Server side:

```powershell
nssm set quicssh-punch AppParameters "-port 44433 -role A -topic punch-XXXX -target 10.144.1.1:4433"
nssm restart quicssh-punch
```

Alternative without NSSM: run it in a terminal/PowerShell session, or use Windows Task Scheduler to start it at boot. `sc.exe create` is not recommended for this binary because it is not a native Windows service.

## 7. Verification

Check service(linux):

```sh
systemctl status quicssh-punch
```

Check OpenWrt:

```sh
/etc/init.d/quicssh-punch status
logread | tail -20
```

Test SSH:

```sh
ssh -v openwrt-punch 'echo ok'
```

Metrics (if enabled):

```sh
curl http://127.0.0.1:9090/metrics
```

## 8. Debugging steps

1. Check the process and service state:

```sh
ps | grep quicssh-punch
systemctl status quicssh-punch          # Linux
/etc/init.d/quicssh-punch status       # OpenWrt
```

2. Read logs:

```sh
journalctl -u quicssh-punch -f          # Linux
logread -f | grep quicssh-punch         # OpenWrt
```

3. Verify the listening UDP port:

```sh
ss -lun | grep 44433
netstat -lun | grep 44433
```

4. Verify STUN endpoint discovery (logs should show `public endpoint ...`).

5. Verify rendezvous traffic:

```sh
curl -s "https://ntfy.sh/<topic>/raw?poll=1&since=10m"
```

6. Capture packets on the punch port:

```sh
tcpdump -ni any udp port 44433
```

7. Test SSH with verbose output:

```sh
ssh -vvv openwrt-punch 'echo ok'
```

8. Check metrics if enabled:

```sh
curl -s http://127.0.0.1:9090/metrics | jq .
```

9. Test direct connectivity and NAT type by running both sides with `-metrics` and comparing `last_peer_packet`.

10. For OpenWrt, check UCI config:

```sh
uci show quicssh-punch
```

## 9. Troubleshooting

- **No connection after 2 minutes**: NAT may be symmetric; use `-relay` or direct public IP.
- **429 rate limited**: ntfy.sh rate limit; reduce traffic, keep heartbeat long, avoid restarting too often.
- **Permission denied on binary**: ensure executable bit (`chmod 755`).
- **Text file busy**: stop service before replacing binary.
- **Wrong architecture**: verify with `uname -m` and rebuild for correct `GOARCH`.
- **Topic leaked**: use `-secret` and a new topic.

## 9. Upgrade

1. Build new binary.
2. Stop service.
3. Replace binary.
4. Start service.
5. Verify SSH and logs.
