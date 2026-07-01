# Local DNS

NovitaBox SDK compatibility works best when all local requests resolve under one domain, for example:

```text
novitabox.local
*.novitabox.local
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
address=/.novitabox.local/127.0.0.1
address=/.novitabox.local/::1
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
Domains=~novitabox.local
EOF

sudo systemctl restart systemd-resolved
```

## Verify

```bash
getent hosts novitabox.local
getent hosts api.novitabox.local
getent hosts sbx-test.novitabox.local
```

All of them should resolve to `127.0.0.1` or `::1`.
