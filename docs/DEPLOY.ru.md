# Руководство по развертыванию (quicssh-punch)

Этот документ описывает развертывание софта `quicssh-punch` на Linux, OpenWrt и Docker.
`quicssh-punch` — UDP hole puncher/relay для работы с `quicssh`.

## 1. Предварительные требования

- Общий rendezvous-топик для обеих сторон (например `punch-<random>`).(чтобы знать какой порт udp открыт на стороне провайдера)
- Одна сторона в **server mode** (`-target` указывает на локальный `quicssh` сервер).
- Другая сторона в **client mode** (без `-target`).
- Одинаковый UDP-порт на обеих сторонах (например `44433`).
- Разрешенный исходящий UDP к STUN и ntfy.sh.

Пример топика:

```sh
TOPIC="punch-$(openssl rand -hex 8)"
echo "$TOPIC"
```

## 2. Сборка

### Linux (локальная машина)

```sh
git clone https://github.com/shafiev/quicssh-punch.git
cd quicssh-punch
go build -o quicssh-punch .
```

### Кросс-сборка для OpenWrt (пример aarch64)

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
  go build -trimpath -ldflags "-s -w" -o quicssh-punch-linux-arm64 .
```

Другие частые цели:

```sh
# OpenWrt x86_64
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o quicssh-punch-linux-amd64 .

# OpenWrt arm (32-bit)
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -o quicssh-punch-linux-armv7 .

# macOS arm64
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o quicssh-punch-macos-arm64 .
```

## 3. Развертывание на Linux (systemd)

### Серверная сторона (роутер/хост с quicssh сервером)

Установка бинарника:

```sh
sudo cp quicssh-punch /usr/local/bin/quicssh-punch
sudo chmod 755 /usr/local/bin/quicssh-punch
```

Создайте `/etc/systemd/system/quicssh-punch.service`:

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

Включение и запуск:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now quicssh-punch
sudo systemctl status quicssh-punch
```

### Клиентская сторона (ноутбук/удаленная машина)

```sh
sudo cp quicssh-punch /usr/local/bin/quicssh-punch
sudo chmod 755 /usr/local/bin/quicssh-punch
/usr/local/bin/quicssh-punch -port 44433 -role B -topic punch-XXXX
```

Или как systemd/user-сервис с `-role B` без `-target`.

### Пример SSH config

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

Подключение:

```sh
ssh openwrt-punch
```

## 4. Развертывание на OpenWrt

### Вариант A: ручная установка бинарника

1. Соберите или скачайте бинарник нужной архитектуры.
2. Скопируйте на роутер:

```sh
scp quicssh-punch-linux-arm64 root@ROUTER:/tmp/quicssh-punch.new
```

Если `scp`/`sftp` недоступны, используйте SSH stdin:

```sh
ssh root@ROUTER 'cat > /tmp/quicssh-punch.new' < quicssh-punch-linux-arm64
```

3. Установите и перезапустите:

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

### Вариант B: пакет OpenWrt

Используйте `packaging/openwrt/` в buildroot или package feed.

```sh
./scripts/feeds update -a
./scripts/feeds install -a
make menuconfig   # Network -> quicssh-punch
make package/quicssh-punch/compile V=s
```

Установите полученный `.ipk`, затем настройте:

```sh
uci set quicssh-punch.main.enabled='1'
uci set quicssh-punch.main.role='A'
uci set quicssh-punch.main.topic='punch-<random>'
uci set quicssh-punch.main.target='10.144.1.1:4433'
uci commit quicssh-punch
/etc/init.d/quicssh-punch enable
/etc/init.d/quicssh-punch start
```

Для клиентской стороны оставьте `target` пустым и установите `role 'B'`.

## 5. Развертывание в Docker

Используйте опубликованный образ или соберите свой:

```sh
docker run -d --network host ghcr.io/shafiev/quicssh-punch:latest \
  -port 44433 -role B -topic punch-XXXX
```

Используйте `--network host` на Linux, чтобы punch использовал NAT-маппинг хоста.

Серверная сторона в Docker:

```sh
docker run -d --network host ghcr.io/shafiev/quicssh-punch:latest \
  -port 44433 -role A -topic punch-XXXX -target 10.144.1.1:4433
```

## 6. Полезные флаги

- `-port`: фиксированный UDP-порт (обязательно).
- `-role`: имя роли, например `A` или `B` (обязательно).
- `-topic`: rendezvous-топик ntfy.sh.
- `-target`: локальный `host:port` для релея (server mode).
- `-peer`: статический адрес пира (тестирование, без rendezvous).
- `-stun` / `-stun2`: STUN-серверы для обнаружения endpoint и определения NAT.
- `-relay`: резервный relay-адрес при зависании прямого punch.
- `-secret`: общий секрет для авторизации rendezvous и обфускации punch.
- `-metrics`: HTTP-адрес для health/metrics JSON.

Пример с секретом и метриками:

```sh
quicssh-punch -port 44433 -role A -topic punch-XXXX \
  -target 10.144.1.1:4433 -secret 'shared-secret' -metrics 127.0.0.1:9090
```

## 6a. Развертывание на macOS (launchd)

Сборка для macOS:

```sh
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o quicssh-punch-macos-arm64 .
# или для Intel Mac:
GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o quicssh-punch-macos-amd64 .
```

Установка бинарника:

```sh
sudo cp quicssh-punch-macos-arm64 /usr/local/bin/quicssh-punch
sudo chmod 755 /usr/local/bin/quicssh-punch
```

Создайте `/Library/LaunchDaemons/com.shafiev.quicssh-punch.plist`:

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

Права и загрузка:

```sh
sudo chown root:wheel /Library/LaunchDaemons/com.shafiev.quicssh-punch.plist
sudo chmod 644 /Library/LaunchDaemons/com.shafiev.quicssh-punch.plist
sudo launchctl bootstrap system /Library/LaunchDaemons/com.shafiev.quicssh-punch.plist
sudo launchctl kickstart -k system/com.shafiev.quicssh-punch
```

Для серверной стороны используйте те же шаги, но с `-role A -target 10.144.1.1:4433`.

## 6b. Развертывание на Windows

Сборка для Windows:

```powershell
$env:GOOS="windows"
$env:GOARCH="amd64"
go build -trimpath -ldflags "-s -w" -o quicssh-punch.exe .
```

`quicssh-punch` — обычное foreground-приложение, поэтому на Windows удобнее запускать его как сервис через **NSSM** (Non-Sucking Service Manager).

Установка NSSM (пример через Chocolatey):

```powershell
choco install nssm -y
```

Установка и запуск сервиса:

```powershell
nssm install quicssh-punch C:\tools\quicssh-punch.exe
nssm set quicssh-punch AppDirectory C:\tools
nssm set quicssh-punch DisplayName "quicssh-punch"
nssm set quicssh-punch Description "quicssh UDP hole-punch relay"
nssm set quicssh-punch Start SERVICE_AUTO_START
nssm set quicssh-punch AppParameters "-port 44433 -role B -topic punch-XXXX"
nssm start quicssh-punch
```

Серверная сторона:

```powershell
nssm set quicssh-punch AppParameters "-port 44433 -role A -topic punch-XXXX -target 10.144.1.1:4433"
nssm restart quicssh-punch
```

Альтернатива без NSSM: запуск в терминале/PowerShell или Windows Task Scheduler при загрузке. `sc.exe create` не рекомендуется, так как бинарник не является нативным Windows-сервисом.

## 7. Проверка

Проверка сервиса  для linux:

```sh
systemctl status quicssh-punch
```

Проверка OpenWrt:

```sh
/etc/init.d/quicssh-punch status
logread | tail -20
```

Тест SSH:

```sh
ssh -v openwrt-punch 'echo ok'
```

Метрики (если включены):

```sh
curl http://127.0.0.1:9090/metrics
```

## 8. Шаги отладки

1. Проверьте процесс и состояние сервиса:

```sh
ps | grep quicssh-punch
systemctl status quicssh-punch          # Linux
/etc/init.d/quicssh-punch status       # OpenWrt
```

2. Прочитайте логи:

```sh
journalctl -u quicssh-punch -f          # Linux
logread -f | grep quicssh-punch         # OpenWrt
```

3. Проверьте слушающий UDP-порт:

```sh
ss -lun | grep 44433
netstat -lun | grep 44433
```

4. Проверьте обнаружение endpoint через STUN (в логах должно быть `public endpoint ...`).

5. Проверьте rendezvous-трафик:

```sh
curl -s "https://ntfy.sh/<topic>/raw?poll=1&since=10m"
```

6. Захватите пакеты на порту punch:

```sh
tcpdump -ni any udp port 44433
```

7. Проверьте SSH с подробным выводом:

```sh
ssh -vvv openwrt-punch 'echo ok'
```

8. Проверьте метрики, если включены:

```sh
curl -s http://127.0.0.1:9090/metrics | jq .
```

9. Для OpenWrt проверьте конфигурацию UCI:

```sh
uci show quicssh-punch
```

## 9. Устранение неполадок

- **Нет соединения через 2 минуты**: NAT может быть симметричным; используйте `-relay` или публичный IP.
- **429 rate limited**: лимит ntfy.sh; уменьшите трафик, держите heartbeat длинным, не перезапускайте слишком часто.
- **Permission denied на бинарнике**: проверьте бит исполнения (`chmod 755`).
- **Text file busy**: остановите сервис перед заменой бинарника.
- **Неверная архитектура**: проверьте `uname -m` и пересоберите с нужным `GOARCH`.
- **Топик утек**: используйте `-secret` и новый топик.

## 9. Обновление

1. Соберите новый бинарник.
2. Остановите сервис.
3. Замените бинарник.
4. Запустите сервис.
5. Проверьте SSH и логи.
