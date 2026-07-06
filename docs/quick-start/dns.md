# Local DNS

NovitaBox SDK compatibility works best when all local requests resolve under one domain, for example:

```text
novitabox.localhost
*.novitabox.localhost
```

The simple single-host setup maps the whole domain to loopback.

```bash
sudo apt-get install -y dnsmasq

sudo tee /etc/dnsmasq.d/novitabox.conf >/dev/null <<'EOF'
listen-address=127.0.0.1
bind-interfaces
no-resolv
server=223.5.5.5
server=202.96.209.133
address=/novitabox.localhost/127.0.0.1
address=/.novitabox.localhost/127.0.0.1
EOF

sudo systemctl restart dnsmasq
```

## systemd-resolved

On systems using `systemd-resolved`, route only the NovitaBox domain to local dnsmasq:

```bash
sudo mkdir -p /etc/systemd/resolved.conf.d
sudo tee /etc/systemd/resolved.conf.d/novitabox.conf >/dev/null <<'EOF'
[Resolve]
DNS=127.0.0.1
Domains=~novitabox.localhost
EOF

sudo systemctl restart systemd-resolved
```

## Verify

```bash
getent hosts novitabox.localhost
getent hosts api.novitabox.localhost
getent hosts sbx-test.novitabox.localhost
```

All of them should resolve to `127.0.0.1`.
