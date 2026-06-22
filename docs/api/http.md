## API Overview

### E2B-Compatible API

```http
POST   /v1/sandboxes
GET    /v1/sandboxes
GET    /v1/sandboxes/{sandbox_id}
DELETE /v1/sandboxes/{sandbox_id}

POST   /v1/sandboxes/{sandbox_id}/pause
POST   /v1/sandboxes/{sandbox_id}/resume

POST   /v1/sandboxes/{sandbox_id}/timeout
POST   /v1/sandboxes/{sandbox_id}/refresh

POST   /v1/templates
GET    /v1/templates
GET    /v1/templates/{template_id}
DELETE /v1/templates/{template_id}
```

### Novita Native API

```http
POST   /v1/sandboxes/{sandbox_id}/poweroff
POST   /v1/sandboxes/{sandbox_id}/poweron
POST   /v1/sandboxes/{sandbox_id}/reboot

POST   /v1/templates/convert

POST   /v1/images
GET    /v1/images
GET    /v1/images/{image_id}
DELETE /v1/images/{image_id}

GET    /v1/runtimes
GET    /v1/runtimes/{runtime_type}
GET    /v1/runtimes/{runtime_type}/capabilities
```