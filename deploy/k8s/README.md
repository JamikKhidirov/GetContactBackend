# Kubernetes deployment

Манифесты рассчитаны на кластер 1.27+ с установленным NGINX Ingress
(опционально) и kube-prometheus-stack (опционально). Namespace — `getcontact`.

---

## 1. Состав

| Файл | Ресурсы |
|---|---|
| `00-namespace.yaml` | Namespace |
| `01-config.yaml` | ConfigMap `getcontact-config`, Secret `getcontact-secrets` (шаблон) |
| `02-postgres.yaml` | Service + StatefulSet + PVC 10Gi |
| `03-redis.yaml` | Service + StatefulSet + PVC 2Gi |
| `04-kafka.yaml` | Service + StatefulSet + PVC 10Gi + Job `kafka-init` |
| `05-api.yaml` | Deployment (2 реплики) + Service + HPA + PDB |
| `06-worker.yaml` | Deployment + Service метрик |
| `07-ingress.yaml` | Ingress + TLS |
| `08-monitoring.yaml` | ServiceMonitor ×2 + PrometheusRule |
| `09-networkpolicy.yaml` | default-deny + DNS + точечные разрешения |
| `kustomization.yaml` | основной набор (без monitoring) |
| `kustomization-monitoring.yaml` | ServiceMonitor/PrometheusRule |

Порядок применения задаёт kustomize; зависимости (Postgres/Redis/Kafka) должны
стать Ready раньше, чем API пройдёт `/readyz`.

---

## 2. Подготовка образов

```bash
export REGISTRY=ghcr.io/your-org
export TAG=1.0.0

docker build -f deploy/docker/Dockerfile --target api    -t $REGISTRY/getcontact-api:$TAG    .
docker build -f deploy/docker/Dockerfile --target worker -t $REGISTRY/getcontact-worker:$TAG .

docker push $REGISTRY/getcontact-api:$TAG
docker push $REGISTRY/getcontact-worker:$TAG
```

Замените образы в `05-api.yaml` и `06-worker.yaml`:

```bash
# Linux
sed -i "s#ghcr.io/your-org/getcontact-api:1.0.0#$REGISTRY/getcontact-api:$TAG#" deploy/k8s/05-api.yaml
sed -i "s#ghcr.io/your-org/getcontact-worker:1.0.0#$REGISTRY/getcontact-worker:$TAG#" deploy/k8s/06-worker.yaml
```

Или используйте kustomize `images:`:

```yaml
images:
  - name: ghcr.io/your-org/getcontact-api
    newName: ghcr.io/your-org/getcontact-api
    newTag: 1.0.0
  - name: ghcr.io/your-org/getcontact-worker
    newName: ghcr.io/your-org/getcontact-worker
    newTag: 1.0.0
```

Для приватного registry:

```bash
kubectl -n getcontact create secret docker-registry ghcr \
  --docker-server=ghcr.io \
  --docker-username=<user> \
  --docker-password=<token> \
  --docker-email=<email>

kubectl -n getcontact patch serviceaccount default \
  -p '{"imagePullSecrets":[{"name":"ghcr"}]}'
```

---

## 3. Namespace и секреты

```bash
kubectl apply -f deploy/k8s/00-namespace.yaml

kubectl -n getcontact create secret generic getcontact-secrets \
  --from-literal=POSTGRES_PASSWORD="$(openssl rand -base64 24)" \
  --from-literal=DATABASE_URL="postgres://getcontact:$(openssl rand -base64 24)@postgres:5432/getcontact?sslmode=disable" \
  --from-literal=JWT_SECRET="$(openssl rand -hex 32)" \
  --from-literal=ADMIN_PASSWORD="$(openssl rand -base64 18)" \
  --dry-run=client -o yaml | kubectl apply -f -
```

> `POSTGRES_PASSWORD` и пароль в `DATABASE_URL` должны совпадать.
> Удобнее сгенерировать один пароль и подставить в оба места:

```bash
PG_PASS="$(openssl rand -base64 24)"
kubectl -n getcontact create secret generic getcontact-secrets \
  --from-literal=POSTGRES_PASSWORD="$PG_PASS" \
  --from-literal=DATABASE_URL="postgres://getcontact:${PG_PASS}@postgres:5432/getcontact?sslmode=disable" \
  --from-literal=JWT_SECRET="$(openssl rand -hex 32)" \
  --from-literal=ADMIN_PASSWORD="$(openssl rand -base64 18)" \
  --dry-run=client -o yaml | kubectl apply -f -
```

`01-config.yaml` содержит шаблон Secret с `CHANGE_ME` — **не применяйте его
поверх реального секрета**. Если применяете файл целиком, сначала отредактируйте
значения, либо применяйте только ConfigMap:

```bash
kubectl apply -f deploy/k8s/01-config.yaml   # перезапишет секрет шаблоном!
```

Безопасный вариант:

```bash
kubectl -n getcontact apply -f - <<'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: getcontact-config
  namespace: getcontact
data:
  APP_ENV: production
  HTTP_ADDR: ":8080"
  METRICS_ADDR: ":9091"
  REDIS_ADDR: redis:6379
  KAFKA_BROKERS: kafka:9092
  KAFKA_TOPIC: getcontact.events
  KAFKA_DLQ_TOPIC: getcontact.events.dlq
  KAFKA_GROUP_ID: getcontact-workers
  KAFKA_ENABLED: "true"
  JWT_ISSUER: getcontact
  JWT_ACCESS_TTL: 15m
  JWT_REFRESH_TTL: 720h
  CORS_ALLOWED_ORIGINS: "https://getcontact.local"
  RATE_LIMIT_ENABLED: "true"
  RATE_LIMIT_PUBLIC_RPM: "120"
  RATE_LIMIT_AUTH_RPM: "20"
  ADMIN_PHONE: "+79990000000"
  ADMIN_NAME: Platform Admin
  DB_MAX_CONNS: "25"
  DB_MIN_CONNS: "5"
  REDIS_CACHE_TTL: 60s
EOF
```

---

## 4. Деплой

```bash
kubectl apply -k deploy/k8s
kubectl -n getcontact get pods -w
```

Ожидаемая последовательность:

1. Postgres, Redis, Kafka — `Running`, probes зелёные.
2. `kafka-init` — `Completed`.
3. API и worker — `Running`, `/readyz` = 200.

Проверка:

```bash
kubectl -n getcontact get pods,svc,hpa,pdb
kubectl -n getcontact rollout status deploy/api
kubectl -n getcontact rollout status deploy/worker
kubectl -n getcontact logs deploy/api --tail=50
kubectl -n getcontact logs job/kafka-init
```

---

## 5. Доступ к API

### Port-forward (быстрый способ)

```bash
kubectl -n getcontact port-forward svc/api 8080:80

curl -s http://localhost:8080/healthz
curl -s http://localhost:8080/readyz
curl -s http://localhost:8080/api/v1/numbers/+79991234567
```

### Ingress

`07-ingress.yaml` создаёт Ingress для `getcontact.local` с TLS secret
`getcontact-tls`. Варианты:

**a) Локально (minikube/kind)** — добавьте host в `hosts` и создайте self-signed:

```bash
# /etc/hosts (Windows: C:\Windows\System32\drivers\etc\hosts)
127.0.0.1 getcontact.local

openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
  -keyout tls.key -out tls.crt \
  -subj "/CN=getcontact.local"

kubectl -n getcontact create secret tls getcontact-tls \
  --cert=tls.crt --key=tls.key
```

**b) cert-manager** — раскомментируйте аннотацию
`cert-manager.io/cluster-issuer: letsencrypt-prod` и убедитесь, что ClusterIssuer
существует.

**c) Без Ingress** — используйте `kubectl port-forward` или `NodePort`.

Проверка Ingress:

```bash
kubectl -n getcontact get ingress
kubectl -n ingress-nginx logs deploy/ingress-nginx-controller --tail=50
```

---

## 6. Мониторинг

Требуется kube-prometheus-stack (или Prometheus Operator + Grafana).

```bash
kubectl apply -k deploy/k8s -f deploy/k8s/kustomization-monitoring.yaml
```

Проверка:

```bash
kubectl -n getcontact get servicemonitor,prometheusrule
```

Если ваш Operator использует другой label селектор (не `release:
kube-prometheus-stack`), измените его в `08-monitoring.yaml`:

```yaml
metadata:
  labels:
    release: <your-prometheus-release>
```

Grafana-дашборд находится в `deploy/docker/grafana/dashboards/getcontact-overview.json`.
Импортируйте его через UI (Dashboards → Import) или создайте ConfigMap с label
`grafana_dashboard: "1"` для Grafana sidecar.

Порты скрейпа:

| Компонент | Порт | Путь |
|---|---|---|
| API | 8080 | `/metrics` |
| Worker | 9091 | `/metrics` |

---

## 7. NetworkPolicy

`09-networkpolicy.yaml` включает:

- **default-deny** для всех pod'ов namespace (ingress + egress);
- DNS egress в `kube-system`;
- ingress к API из `ingress-nginx` и `monitoring` (плюс порт 8080 для kubelet-проб);
- ingress к worker metrics из `monitoring`;
- egress api/worker только к Postgres (5432), Redis (6379), Kafka (9092);
- egress Kafka-брокеров к самим себе (9092/9093) для inter-broker и controller quorum;
- egress `kafka-init` только к Kafka (9092);
- ingress к Postgres/Redis/Kafka — отдельными политиками, только из namespace-пула приложения.

Важно:

- Если ваш CNI не поддерживает NetworkPolicy (например, старый flannel),
  политики не применятся — трафик продолжит ходить.
- Если Prometheus находится в другом namespace, поправьте
  `namespaceSelector.matchLabels.kubernetes.io/metadata.name`.
- Kafka-init Job ходит к Kafka — учтён в разрешённых pod'ах.

---

## 8. Масштабирование и обновления

```bash
# HPA
kubectl -n getcontact get hpa api -w

# Ручное масштабирование (HPA перезапишет)
kubectl -n getcontact scale deploy/api --replicas=3

# Новый образ
kubectl -n getcontact set image deploy/api api=$REGISTRY/getcontact-api:$NEW_TAG
kubectl -n getcontact rollout status deploy/api

# Откат
kubectl -n getcontact rollout undo deploy/api
kubectl -n getcontact rollout history deploy/api
```

API stateless; worker'ы объединены в consumer group и сами балансируют партиции.
При масштабировании worker'ов не превышайте число партиций (3), иначе лишние
реплики будут простаивать.

---

## 9. Диагностика

```bash
# События namespace
kubectl -n getcontact get events --sort-by=.lastTimestamp

# Описание проблемного pod'а
kubectl -n getcontact describe pod <pod>

# Логи
kubectl -n getcontact logs deploy/api --tail=200
kubectl -n getcontact logs deploy/worker --tail=200
kubectl -n getcontact logs kafka-0 --tail=200

# Проверка readiness изнутри
kubectl -n getcontact exec deploy/api -- wget -qO- http://127.0.0.1:8080/readyz

# Проверка БД
kubectl -n getcontact exec -it postgres-0 -- psql -U getcontact -d getcontact -c '\dt'

# Проверка Kafka
kubectl -n getcontact exec kafka-0 -- \
  /opt/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --list
```

| Симптом | Причина | Решение |
|---|---|---|
| `ImagePullBackOff` | приватный registry / неверный tag | imagePullSecret, проверьте tag |
| API CrashLoopBackOff | секрет/DSN/миграция | `kubectl logs`, проверьте `/readyz` |
| `kafka-init` Failed | Kafka ещё не готова | Job ретраится; проверьте логи Kafka |
| `readyz` 503 | Postgres/Redis/Kafka | проверьте Service/DNS/NetworkPolicy |
| PVC Pending | нет StorageClass | `kubectl get storageclass`, задайте class |
| Grafana без данных | label ServiceMonitor | поправьте `release` label |
| HPA не масштабирует | metrics-server не установлен | установите metrics-server |

---

## 10. Удаление

```bash
# Удалить все ресурсы namespace (PVC останутся)
kubectl delete -k deploy/k8s
kubectl delete -k deploy/k8s -f deploy/k8s/kustomization-monitoring.yaml

# Полная очистка, включая данные
kubectl delete namespace getcontact
```

> `kubectl delete namespace` удаляет и PVC, если StorageClass поддерживает
> reclaim policy `Delete`. Для production сделайте бэкап заранее.

---

## 11. Production-замечания

- StatefulSet'ы Postgres/Redis/Kafka — baseline для staging. Для production
  используйте managed-сервисы или операторы (CloudNativePG, Strimzi,
  Redis Operator).
- Kafka: 3+ брокера, RF=3, `min.insync.replicas=2`, отдельные топики DLQ.
- Postgres: HA, PITR-бэкапы, connection pooler (PgBouncer) при большом числе реплик.
- Секреты — External Secrets / Vault / Sealed Secrets, не `stringData` в git.
- Образы — digest-pinned, сканируются Trivy/Grype, подписаны cosign.
- Добавьте `topologySpreadConstraints` для API по зонам.
- Настройте Alertmanager-маршруты и on-call runbook (см. `docs/OBSERVABILITY.md`).
