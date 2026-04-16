# TripKit Infrastructure Deployment — Rapport pour opencode

## Contexte

TripKit est une PWA pour planifier des voyages en famille. Il est splitté en 2 repos :
- **Backend** : `rjullien/tripkit-backend` — Go API (chi + GORM + SQLite), 47 tests
- **Frontend** : `rjullien/tripkit-frontend` — Vanilla JS/HTML/CSS + nginx

L'auth sera gérée par **Authelia** (centralisée, réutilisable pour les futures apps famille).

Ce rapport décrit tout ce qu'il faut mettre en place dans `BaptTF/vps-infra`.

---

## 1. Images Docker — À publier d'abord

**Aucune image n'est encore publiée sur GHCR.** Le CI ne build que sur `release`.

### Action requise :
```bash
# Backend — créer la première release
gh release create v0.1.0 --repo rjullien/tripkit-backend \
  --title "v0.1.0" --notes "Initial release — Go API"

# Frontend — créer la première release  
gh release create v0.1.0 --repo rjullien/tripkit-frontend \
  --title "v0.1.0" --notes "Initial release — PWA"
```

Les GitHub Actions vont build et push :
- `ghcr.io/rjullien/tripkit-backend:0.1.0` + `:latest`
- `ghcr.io/rjullien/tripkit-frontend:0.1.0` + `:latest`

**⚠️ Vérifier que les packages GHCR sont visibles par le cluster** (même pattern que `voyage-app` — `ghcr-login-secret` via Infisical).

---

## 2. Architecture cible

```
Internet
    │
    ▼
Traefik (k3s)
    │
    ├── auth.juju.bapttf.com ──→ Authelia (namespace: authelia)
    │                              - Login portal
    │                              - User DB (YAML file)
    │                              - Session store (SQLite)
    │                              - 2FA: TOTP + WebAuthn/Passkeys
    │
    └── tripkit.bapttf.com ──→ Traefik forwardAuth middleware
                                    │
                                    ▼
                              Authelia vérifie le cookie session
                                    │
                              ┌─────┴──────┐
                              │  Frontend   │ (nginx :80)
                              │  /api/* ────┼──→ Backend (Go :3001)
                              └─────────────┘
                                    │
                                 SQLite PVC
```

### Namespaces
- `authelia` — Authelia (shared, réutilisable pour futures apps)
- `tripkit` — Backend + Frontend TripKit

---

## 3. Authelia — Namespace `authelia`

### 3.1 Pourquoi Authelia (et pas oauth2-proxy comme voyage)

`voyage` utilise un oauth2-proxy sidecar avec GitHub OAuth — ça marche pour une app isolée.
Pour TripKit + futures apps famille, on veut :
- **Gestion d'identité centralisée** (users, groupes, rôles)
- **Policies d'accès par app/domaine** (Nicole voit TripKit mais pas l'admin)
- **2FA intégré** (TOTP, WebAuthn/passkeys → Face ID sur mobile)
- **Un seul login** pour toutes les apps `*.juju.bapttf.com`
- **OIDC provider** pour les apps qui veulent un auth flow standard

Authelia = 1 container, ~30 Mo RAM, config YAML, intégration Traefik native.

### 3.2 Fichiers à créer dans `vps-infra`

#### `apps/authelia-app.yaml` (ArgoCD Application)
```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: authelia
  namespace: argocd
  annotations:
    argocd.argoproj.io/manifest-generate-paths: .
spec:
  project: system   # ou default — system si on considère l'auth comme infra
  source:
    repoURL: 'https://github.com/BaptTF/vps-infra.git'
    targetRevision: HEAD
    path: system/authelia    # system/ car c'est de l'infra partagée
  destination:
    server: 'https://kubernetes.default.svc'
    namespace: authelia
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - CreateNamespace=true
```

#### `system/authelia/kustomization.yaml`
```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - deployment.yaml
  - service.yaml
  - ingressroute.yaml
  - pvc.yaml
  - configmap.yaml
  - infisical-secret.yaml
  - middleware.yaml
```

#### `system/authelia/configmap.yaml`
Contient `configuration.yml` et `users_database.yml` en ConfigMap.

**configuration.yml :**
```yaml
theme: dark

server:
  address: tcp://0.0.0.0:9091/

log:
  level: info

authentication_backend:
  file:
    path: /config/users_database.yml
    password:
      algorithm: argon2id
      iterations: 3
      memory: 65536
      parallelism: 4
      key_length: 32
      salt_length: 16

session:
  name: authelia_session
  same_site: lax
  expiration: 7d
  inactivity: 3d
  remember_me: 1M
  cookies:
    - domain: bapttf.com
      authelia_url: https://auth.juju.bapttf.com
      default_redirection_url: https://tripkit.bapttf.com

storage:
  local:
    path: /data/db.sqlite3

notifier:
  filesystem:
    filename: /data/notification.txt
  # En prod, remplacer par SMTP pour les password resets :
  # smtp:
  #   host: smtp.gmail.com
  #   port: 587
  #   username: xxx
  #   sender: "Famille Juju <noreply@bapttf.com>"

access_control:
  default_policy: deny
  rules:
    - domain: tripkit.bapttf.com
      policy: one_factor
      subject: "group:family"
    - domain: "*.juju.bapttf.com"
      policy: one_factor
      subject: "group:family"
    - domain: "*.bapttf.com"
      policy: one_factor
      subject: "group:admin"

totp:
  issuer: juju.bapttf.com
  period: 30

webauthn:
  display_name: Famille Juju
```

**users_database.yml :**
```yaml
users:
  rene:
    disabled: false
    displayname: René
    password: "$argon2id$v=19$m=65536,t=3,p=4$GENERATED_HASH"
    email: rene.jullien@gmail.com
    groups: [family, admin]
  baptiste:
    disabled: false
    displayname: Baptiste
    password: "$argon2id$v=19$m=65536,t=3,p=4$GENERATED_HASH"
    email: baptiste.jullien06@gmail.com
    groups: [family, admin]
  nicole:
    disabled: false
    displayname: Nicole
    password: "$argon2id$v=19$m=65536,t=3,p=4$GENERATED_HASH"
    email: nicol.jullien@gmail.com
    groups: [family]
  alexandre:
    disabled: false
    displayname: Alexandre
    password: "$argon2id$v=19$m=65536,t=3,p=4$GENERATED_HASH"
    email: alexandre.jullien06@gmail.com
    groups: [family]
  camille:
    disabled: false
    displayname: Camille
    password: "$argon2id$v=19$m=65536,t=3,p=4$GENERATED_HASH"
    email: jullien.camille06@gmail.com
    groups: [family]
```

**⚠️ Générer les hashes :**
```bash
docker run --rm authelia/authelia:latest \
  authelia crypto hash generate argon2 --password 'le-password'
```

**Note :** Les users_database.yml contient les hashes — soit on le met en ConfigMap (acceptable car les hashes sont one-way), soit on le gère via Infisical si on veut zero-trust. À toi de voir.

#### `system/authelia/deployment.yaml`
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: authelia
  namespace: authelia
  labels:
    app: authelia
spec:
  replicas: 1
  selector:
    matchLabels:
      app: authelia
  template:
    metadata:
      labels:
        app: authelia
    spec:
      containers:
        - name: authelia
          image: authelia/authelia:4.39
          ports:
            - containerPort: 9091
          env:
            - name: AUTHELIA_STORAGE_ENCRYPTION_KEY
              valueFrom:
                secretKeyRef:
                  name: authelia-secrets
                  key: storage-encryption-key
            - name: AUTHELIA_SESSION_SECRET
              valueFrom:
                secretKeyRef:
                  name: authelia-secrets
                  key: session-secret
            - name: AUTHELIA_IDENTITY_VALIDATION_RESET_PASSWORD_JWT_SECRET
              valueFrom:
                secretKeyRef:
                  name: authelia-secrets
                  key: jwt-secret
          volumeMounts:
            - name: config
              mountPath: /config
              readOnly: true
            - name: data
              mountPath: /data
          resources:
            requests:
              memory: 32Mi
              cpu: 10m
            limits:
              memory: 128Mi
              cpu: 200m
          livenessProbe:
            httpGet:
              path: /api/health
              port: 9091
            initialDelaySeconds: 5
            periodSeconds: 30
          readinessProbe:
            httpGet:
              path: /api/health
              port: 9091
            initialDelaySeconds: 3
            periodSeconds: 10
      volumes:
        - name: config
          configMap:
            name: authelia-config
        - name: data
          persistentVolumeClaim:
            claimName: authelia-data
```

#### `system/authelia/service.yaml`
```yaml
apiVersion: v1
kind: Service
metadata:
  name: authelia
  namespace: authelia
spec:
  selector:
    app: authelia
  ports:
    - port: 9091
      targetPort: 9091
```

#### `system/authelia/pvc.yaml`
```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: authelia-data
  namespace: authelia
spec:
  accessModes: [ReadWriteOnce]
  resources:
    requests:
      storage: 256Mi
```

#### `system/authelia/ingressroute.yaml`
```yaml
apiVersion: traefik.io/v1alpha1
kind: IngressRoute
metadata:
  name: authelia
  namespace: authelia
spec:
  entryPoints:
    - websecure
  routes:
    - match: Host(`auth.juju.bapttf.com`)
      kind: Rule
      services:
        - name: authelia
          port: 9091
  tls: {}
```

#### `system/authelia/middleware.yaml`
```yaml
# Ce middleware est référencé cross-namespace par les workloads
apiVersion: traefik.io/v1alpha1
kind: Middleware
metadata:
  name: forwardauth-authelia
  namespace: authelia
spec:
  forwardAuth:
    address: http://authelia.authelia.svc.cluster.local:9091/api/authz/forward-auth
    trustForwardHeader: true
    authResponseHeaders:
      - Remote-User
      - Remote-Groups
      - Remote-Email
      - Remote-Name
```

#### `system/authelia/infisical-secret.yaml`
```yaml
apiVersion: secrets.infisical.com/v1alpha1
kind: InfisicalSecret
metadata:
  name: authelia-secrets-sync
  namespace: authelia
  annotations:
    argocd.argoproj.io/sync-wave: "-1"
spec:
  hostAPI: https://app.infisical.com/api
  authentication:
    universalAuth:
      secretsScope:
        projectSlug: infrastructure
        envSlug: prod
        secretsPath: /authelia
      credentialsRef:
        secretName: infisical-universal-auth
        secretNamespace: infisical
  managedKubeSecretReferences:
    - secretName: authelia-secrets
      secretNamespace: authelia
      secretType: Opaque
      creationPolicy: Owner
```

**Secrets Infisical à créer** (path `/authelia`) :
| Key | Value | How to generate |
|-----|-------|-----------------|
| `storage-encryption-key` | 128-char hex | `openssl rand -hex 64` |
| `session-secret` | 128-char hex | `openssl rand -hex 64` |
| `jwt-secret` | 128-char hex | `openssl rand -hex 64` |

---

## 4. TripKit — Namespace `tripkit`

### 4.1 Fichiers à créer dans `vps-infra`

#### `apps/tripkit.yaml` (ArgoCD Application)
```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: tripkit
  namespace: argocd
  annotations:
    argocd.argoproj.io/manifest-generate-paths: .
spec:
  project: default
  source:
    repoURL: 'https://github.com/BaptTF/vps-infra.git'
    targetRevision: HEAD
    path: workloads/tripkit
  destination:
    server: 'https://kubernetes.default.svc'
    namespace: tripkit
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - CreateNamespace=true
```

#### `workloads/tripkit/kustomization.yaml`
```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - backend-deployment.yaml
  - backend-service.yaml
  - frontend-deployment.yaml
  - frontend-service.yaml
  - ingressroute.yaml
  - pvc.yaml
  - infisical-secret.yaml
```

#### `workloads/tripkit/backend-deployment.yaml`
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: tripkit-backend
  namespace: tripkit
  labels:
    app: tripkit-backend
spec:
  replicas: 1
  selector:
    matchLabels:
      app: tripkit-backend
  template:
    metadata:
      labels:
        app: tripkit-backend
    spec:
      imagePullSecrets:
        - name: ghcr-login-secret
      containers:
        - name: backend
          image: ghcr.io/rjullien/tripkit-backend:latest
          ports:
            - containerPort: 3001
          env:
            - name: TRIPKIT_API_TOKEN
              valueFrom:
                secretKeyRef:
                  name: tripkit-secrets
                  key: api-token
            - name: DB_PATH
              value: /data/tripkit.db
          volumeMounts:
            - name: data
              mountPath: /data
          resources:
            requests:
              memory: 32Mi
              cpu: 10m
            limits:
              memory: 128Mi
              cpu: 200m
          livenessProbe:
            httpGet:
              path: /health
              port: 3001
            initialDelaySeconds: 3
            periodSeconds: 30
          readinessProbe:
            httpGet:
              path: /health
              port: 3001
            initialDelaySeconds: 2
            periodSeconds: 10
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: tripkit-data
```

#### `workloads/tripkit/backend-service.yaml`
```yaml
apiVersion: v1
kind: Service
metadata:
  name: tripkit-backend
  namespace: tripkit
spec:
  selector:
    app: tripkit-backend
  ports:
    - port: 3001
      targetPort: 3001
```

#### `workloads/tripkit/frontend-deployment.yaml`
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: tripkit-frontend
  namespace: tripkit
  labels:
    app: tripkit-frontend
spec:
  replicas: 1
  selector:
    matchLabels:
      app: tripkit-frontend
  template:
    metadata:
      labels:
        app: tripkit-frontend
    spec:
      imagePullSecrets:
        - name: ghcr-login-secret
      containers:
        - name: frontend
          image: ghcr.io/rjullien/tripkit-frontend:latest
          ports:
            - containerPort: 80
          resources:
            requests:
              memory: 16Mi
              cpu: 5m
            limits:
              memory: 64Mi
              cpu: 100m
          livenessProbe:
            httpGet:
              path: /
              port: 80
            initialDelaySeconds: 3
            periodSeconds: 30
```

#### `workloads/tripkit/frontend-service.yaml`
```yaml
apiVersion: v1
kind: Service
metadata:
  name: tripkit-frontend
  namespace: tripkit
spec:
  selector:
    app: tripkit-frontend
  ports:
    - port: 80
      targetPort: 80
```

#### `workloads/tripkit/ingressroute.yaml`
```yaml
apiVersion: traefik.io/v1alpha1
kind: IngressRoute
metadata:
  name: tripkit
  namespace: tripkit
spec:
  entryPoints:
    - websecure
  routes:
    - match: Host(`tripkit.bapttf.com`)
      kind: Rule
      middlewares:
        - name: forwardauth-authelia
          namespace: authelia      # cross-namespace middleware reference
      services:
        - name: tripkit-frontend
          port: 80
  tls: {}
```

**⚠️ Cross-namespace middleware :** Traefik doit avoir `allowCrossNamespace: true` dans sa config. Vérifier dans `system/traefik/values.yaml`. Si pas activé, il faut l'ajouter :
```yaml
# Dans les values Traefik Helm
providers:
  kubernetesCRD:
    allowCrossNamespace: true
```

#### `workloads/tripkit/pvc.yaml`
```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: tripkit-data
  namespace: tripkit
spec:
  accessModes: [ReadWriteOnce]
  resources:
    requests:
      storage: 1Gi
```

#### `workloads/tripkit/infisical-secret.yaml`
```yaml
apiVersion: secrets.infisical.com/v1alpha1
kind: InfisicalSecret
metadata:
  name: tripkit-secrets-sync
  namespace: tripkit
  annotations:
    argocd.argoproj.io/sync-wave: "-1"
spec:
  hostAPI: https://app.infisical.com/api
  authentication:
    universalAuth:
      secretsScope:
        projectSlug: infrastructure
        envSlug: prod
        secretsPath: /tripkit
      credentialsRef:
        secretName: infisical-universal-auth
        secretNamespace: infisical
  managedKubeSecretReferences:
    - secretName: tripkit-secrets
      secretNamespace: tripkit
      secretType: Opaque
      creationPolicy: Owner
---
apiVersion: secrets.infisical.com/v1alpha1
kind: InfisicalSecret
metadata:
  name: github-secrets
  namespace: tripkit
  annotations:
    argocd.argoproj.io/sync-wave: "-1"
spec:
  hostAPI: https://app.infisical.com/api
  authentication:
    universalAuth:
      secretsScope:
        projectSlug: infrastructure
        envSlug: prod
        secretsPath: /github/r
      credentialsRef:
        secretName: infisical-universal-auth
        secretNamespace: infisical
  managedKubeSecretReferences:
    - secretName: ghcr-login-secret
      secretNamespace: tripkit
      secretType: kubernetes.io/dockerconfigjson
      creationPolicy: Owner
```

**Secrets Infisical à créer** (path `/tripkit`) :
| Key | Value | Description |
|-----|-------|-------------|
| `api-token` | Random string | Backend Bearer token for M2M API calls |

Le GHCR pull secret réutilise le même path `/github/r` que voyage.

---

## 5. DNS

Ajouter dans Cloudflare (ou là où sont les DNS de bapttf.com) :
- `tripkit.bapttf.com` → IP du cluster (ou CNAME existant)
- `auth.juju.bapttf.com` → IP du cluster (ou CNAME existant)

---

## 6. Frontend nginx → Backend proxy

Le frontend nginx doit router `/api/*` vers le backend. Deux options :

### Option A — nginx dans l'image frontend (actuel)
Le `nginx.conf` dans le repo frontend fait déjà :
```nginx
location /api/ {
    proxy_pass http://tripkit-backend:3001;
}
```
Ça marche si le service `tripkit-backend` est dans le même namespace.

### Option B — Deux routes IngressRoute
Si on veut séparer au niveau Traefik :
```yaml
routes:
  - match: Host(`tripkit.bapttf.com`) && PathPrefix(`/api`)
    kind: Rule
    middlewares:
      - name: forwardauth-authelia
        namespace: authelia
    services:
      - name: tripkit-backend
        port: 3001
  - match: Host(`tripkit.bapttf.com`)
    kind: Rule
    middlewares:
      - name: forwardauth-authelia
        namespace: authelia
    services:
      - name: tripkit-frontend
        port: 80
```

**Recommandation :** Option A (nginx proxy_pass) — plus simple, le frontend et backend sont dans le même namespace, pas besoin de compliquer le routing Traefik.

---

## 7. Ordre de déploiement

1. **Secrets Infisical** — Créer les paths `/authelia` et `/tripkit` avec les secrets
2. **DNS** — `tripkit.bapttf.com` + `auth.juju.bapttf.com`
3. **Images** — Créer les releases v0.1.0 sur les 2 repos, attendre que le CI push les images
4. **Traefik** — Vérifier `allowCrossNamespace: true`
5. **Authelia** — `kubectl apply` ou ArgoCD sync
6. **TripKit** — `kubectl apply` ou ArgoCD sync
7. **Test** — Ouvrir `tripkit.bapttf.com` → doit rediriger vers `auth.juju.bapttf.com`

---

## 8. Checklist finale

- [ ] Créer release v0.1.0 backend (`gh release create`)
- [ ] Créer release v0.1.0 frontend (`gh release create`)
- [ ] Vérifier images sur GHCR
- [ ] Créer secrets Infisical `/authelia` (3 keys)
- [ ] Créer secrets Infisical `/tripkit` (1 key: api-token)
- [ ] Générer les password hashes Authelia pour la famille
- [ ] DNS: `tripkit.bapttf.com` + `auth.juju.bapttf.com`
- [ ] Vérifier Traefik `allowCrossNamespace: true`
- [ ] Deployer Authelia (ArgoCD app)
- [ ] Deployer TripKit (ArgoCD app)
- [ ] Test flow complet : tripkit.bapttf.com → login → accès
- [ ] Envoyer les credentials à la famille

---

## 9. Schéma des fichiers dans vps-infra

```
vps-infra/
├── apps/
│   ├── authelia-app.yaml          # NEW — ArgoCD Application
│   └── tripkit.yaml               # NEW — ArgoCD Application
├── system/
│   └── authelia/                  # NEW
│       ├── kustomization.yaml
│       ├── deployment.yaml
│       ├── service.yaml
│       ├── ingressroute.yaml
│       ├── pvc.yaml
│       ├── configmap.yaml
│       ├── middleware.yaml         # forwardAuth — referenced cross-namespace
│       └── infisical-secret.yaml
└── workloads/
    └── tripkit/                   # NEW
        ├── kustomization.yaml
        ├── backend-deployment.yaml
        ├── backend-service.yaml
        ├── frontend-deployment.yaml
        ├── frontend-service.yaml
        ├── ingressroute.yaml      # References authelia middleware
        ├── pvc.yaml
        └── infisical-secret.yaml
```
