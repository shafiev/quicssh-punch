# OpenWrt package

Everything the package needs is in this directory:

```
packaging/openwrt/
├── Makefile                      # package recipe (downloads the tag, builds with Go)
├── files/
│   ├── quicssh-punch.init        # procd service, reads /etc/config/quicssh-punch
│   └── quicssh-punch.config      # UCI config (disabled by default)
└── luci-app-quicssh-punch/       # optional LuCI web interface
    ├── Makefile
    ├── luasrc/controller/quicssh_punch.lua
    └── luasrc/model/cbi/quicssh_punch.lua
```

## Where to put it

In a buildroot or the `packages` feed, copy this directory to:

```
feeds/packages/net/quicssh-punch/
```

then enable it:

```sh
./scripts/feeds update -a
./scripts/feeds install -a
make menuconfig     # Network -> quicssh-punch
make package/quicssh-punch/compile V=s
```

The resulting `.ipk` is under `bin/packages/<arch>/base/` (or `packages/`).
It installs:

| File | Purpose |
|---|---|
| `/usr/bin/quicssh-punch` | the relay (statically linked) |
| `/etc/init.d/quicssh-punch` | procd service, enabled but idle until configured |
| `/etc/config/quicssh-punch` | UCI config, `enabled '0'` by default |

## Configure on the router

```
uci set quicssh-punch.main.enabled='1'
uci set quicssh-punch.main.role='A'
uci set quicssh-punch.main.topic='punch-<random>'
uci set quicssh-punch.main.target='10.144.1.1:4433'
uci commit quicssh-punch
/etc/init.d/quicssh-punch enable
/etc/init.d/quicssh-punch start
```

The same package works for the client side: leave `target` empty and set
`role 'B'`.

This package ships only the punch relay. `quicssh` itself comes from
<https://github.com/moul/quicssh> and is not packaged here.
