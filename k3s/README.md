# TripKit — k3s Deployment Guide

## Architecture
```
Internet
    │
    ▼
Traefik (k3s built-in)
    │
    ├── auth.juju.bapttf.com ──→ Authelia (login portal)
    │
    └── tripkit.bapttf.com ──→ [forwardAuth middleware]
                                    │
                                    ├── /* ──→ Frontend (nginx :80)
                                    └── /api/* ──→ Backend (Go :3001)  [via nginx proxy_pass]
                                                     │
                                                SQLite (PVC)
```

## Prerequisites
- k3s cluster with Traefik
- cert-manager (for TLS) or manual TLS secrets
- Domains: `tripkit.bapttf.com`, `auth.juju.bapttf.com`

## Deploy Order

### 1. Authelia (auth layer)
```bash
kubectl apply -f authelia/namespace.yaml

# Create secrets (edit first!)
cp authelia/secret.yaml.example authelia/secret.yaml
# Generate: openssl rand -hex 64 (×3 for each secret)
kubectl apply -f authelia/secret.yaml

# Create ConfigMap from config files
kubectl -n authelia create configmap authelia-config \
  --from-file=configuration.yml=authelia/configuration.yml \
  --from-file=users_database.yml=authelia/users_database.yml

kubectl apply -f authelia/pvc.yaml
kubectl apply -f authelia/deployment.yaml
kubectl apply -f authelia/service.yaml
kubectl apply -f authelia/ingressroute.yaml
```

### 2. TripKit
```bash
kubectl apply -f namespace.yaml

cp secret.yaml.example secret.yaml
kubectl apply -f secret.yaml

kubectl apply -f pvc.yaml
kubectl apply -f configmap.yaml
kubectl apply -f backend-deployment.yaml
kubectl apply -f backend-service.yaml
kubectl apply -f frontend-deployment.yaml
kubectl apply -f frontend-service.yaml
kubectl apply -f middleware-authelia.yaml
kubectl apply -f ingressroute.yaml
```

## User Management

### Add a user
```bash
# Generate password hash
docker run --rm authelia/authelia:latest \
  authelia crypto hash generate argon2 --password 'their-password'

# Add to users_database.yml, then update ConfigMap:
kubectl -n authelia create configmap authelia-config \
  --from-file=configuration.yml=authelia/configuration.yml \
  --from-file=users_database.yml=authelia/users_database.yml \
  --dry-run=client -o yaml | kubectl apply -f -

# Restart Authelia to pick up changes
kubectl -n authelia rollout restart deployment/authelia
```

### WebAuthn / Passkeys
After first login with password, users can register WebAuthn devices (Face ID, fingerprint, security key) at `https://auth.juju.bapttf.com/settings`. After that → passwordless login.

## Secrets Summary

| Namespace | Secret | Keys |
|-----------|--------|------|
| `authelia` | `authelia-secrets` | `storage-encryption-key`, `session-secret`, `jwt-secret` |
| `tripkit` | `tripkit-secrets` | `api-token` (backend Bearer token) |

## Images
- **Backend**: `ghcr.io/rjullien/tripkit-backend:<version>`
- **Frontend**: `ghcr.io/rjullien/tripkit-frontend:<version>`
- **Auth**: `authelia/authelia:latest`

## Troubleshooting
```bash
# Authelia
kubectl -n authelia logs -l app=authelia
kubectl -n authelia port-forward svc/authelia 9091:9091
curl http://localhost:9091/api/health

# TripKit
kubectl -n tripkit logs -l app=tripkit-backend
kubectl -n tripkit port-forward svc/tripkit-backend 3001:3001
curl http://localhost:3001/health
```

## Future Apps
To protect a new app with Authelia:
1. Add the domain rule in `authelia/configuration.yml` → `access_control.rules`
2. Add `middlewares: [{name: forwardauth-authelia, namespace: tripkit}]` to its IngressRoute
3. That's it — auth is centralized
