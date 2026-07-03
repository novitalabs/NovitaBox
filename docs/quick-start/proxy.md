# Reverse Proxy

NovitaBox has two local HTTP services:

- `boxapi` on `127.0.0.1:8080`
- `boxproxy` on `127.0.0.1:8082`

SDKs expect one domain for the API and wildcard domains for sandbox traffic. The recommended local setup uses Caddy with an internal CA.

## Caddy

```bash
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
    | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg

curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
    | sudo tee /etc/apt/sources.list.d/caddy-stable.list

sudo chmod o+r /usr/share/keyrings/caddy-stable-archive-keyring.gpg
sudo chmod o+r /etc/apt/sources.list.d/caddy-stable.list

sudo apt update
sudo apt install -y caddy


sudo tee /etc/caddy/Caddyfile >/dev/null <<'EOF'
{
  local_certs
}

api.novitabox.localhost {
  tls internal
  reverse_proxy 127.0.0.1:8080
}

*.novitabox.localhost {
  tls internal
  reverse_proxy 127.0.0.1:8082
}
EOF


sudo systemctl restart caddy
sudo systemctl status caddy --no-pager


sudo cp /var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt /usr/local/share/ca-certificates/caddy-local.crt
sudo update-ca-certificates
```

## Routing Model

```text
https://novitabox.localhost
  -> 127.0.0.1:8080

https://<sandbox-or-port>.novitabox.localhost
  -> 127.0.0.1:8082
```

`boxproxy` resolves the sandbox id from the request host or headers, finds the sandbox network slot from the local database, and forwards traffic to the guest agent through the sandbox host access IP.

## Health Checks

```bash
curl -k https://novitabox.localhost/health
curl http://127.0.0.1:8082/healthz
```

If HTTPS clients fail certificate verification, make sure the Caddy local CA has been installed:

```bash
sudo cp /var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt \
  /usr/local/share/ca-certificates/caddy-local.crt
sudo update-ca-certificates
```
