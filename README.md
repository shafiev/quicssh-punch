# quicssh-punch
The software to make a udp punch hole to connect to device via QuicSSH  if you don't have a Public IPv4 address

Builted on go only on go std libs with any deps and used the free ntfy.sh

Example : 
/usr/local/bin/quicssh-punch -port 44433 -role B -topic punch-fXXXXXXXXXXXXXXXXXX

The .ssh/config
```
Host openwrt-punch
	HostName 10.144.1.1
	User root
	IdentityFile ~/.ssh/id_rsa
	IdentitiesOnly yes
	BatchMode yes
	ConnectTimeout 30
	ProxyCommand /usr/local/bin/quicssh client --addr 127.0.0.1:44433
```

and then connect to it via a standart ssh 
```
ssh openwrt-punch
```

## Container image

Published to GitHub Packages as `ghcr.io/shafiev/quicssh-punch` (linux/amd64, linux/arm64):

```sh
docker run -d --network host ghcr.io/shafiev/quicssh-punch:latest \
  -port 44433 -role B -topic punch-fXXXXXXXXXXXXXXXXXX
```

Use `--network host` on Linux: the punch must use the host's NAT mapping.
