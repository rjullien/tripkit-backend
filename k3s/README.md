# TripKit — k3s Deployment Guide

## Architecture
```
Internet → Ingress (Traefik) → [Auth Middleware] → Frontend (nginx) → Backend (Go)
                                                                        ↓
                                                                   SQLite (PVC)
```

## Quick Deploy
```bash
kubectl apply -f namespace.yaml
cp secret.yaml.example secret.yaml  # edit with real values
kubectl apply -f secret.yaml
kubectl apply -f pvc.yaml
kubectl apply -f configmap.yaml
kubectl apply -f backend-deployment.yaml
kubectl apply -f backend-service.yaml
kubectl apply -f frontend-deployment.yaml
kubectl apply -f frontend-service.yaml
kubectl apply -f ingress.yaml
```

## Secrets
| Key | Description |
|-----|-------------|
| `api-token` | Generate: `openssl rand -hex 32` |

## Images
- **Backend**: `ghcr.io/rjullien/tripkit-backend:<version>`
- **Frontend**: `ghcr.io/rjullien/tripkit-frontend:<version>`
