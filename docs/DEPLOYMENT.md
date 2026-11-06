# Deployment

Два поддерживаемых пути: **Docker Compose** (локально/демо) и **Kubernetes**
(staging/production baseline). Оба используют один и тот же образ с двумя target'ами:
`api` и `worker`.

---

## 1. Требования

| Компонент | Версия |
|---|---|
| Go | 1.26+ (образ собирается на `golang:1.26-alpine`) |
| Docker Engine | 24+ с Compose v2 |
| Kubernetes | 1.27+ (для HPA v2, PDB v1) |
| kubectl | 1.27+ |
| PostgreSQL | 16 |
| Redis | 7 |
| Kafka | 3.7 (KRaft, без ZooKeeper) |

Go-модули не требуют CGO. Бинарники статические (`CGO_ENABLED=0`), образ работает
на `scratch`-подобной основе с non-root пользователем.

---

## 2. Переменные окружения

Полный список с дефолтами — в [`.env.example`](../.env.example).

### Критичные

| Переменная | Обязательна | Пример | Комментарий |
|---|---|---|---|
| `DATABASE_URL` | да | `postgres://getcontact:pass@postgres:5432/getcontact?sslmode=disable` | pgx DSN |
| `REDIS_ADDR` | да | `redis:6379` | хост:порт |
| `JWT_SECRET` | да | 32+ случайных символа | в production короче 32 → сервис не стартует |
| `KAFKA_BROKERS` | да | `kafka:9092` | через запятую |
| `ADMIN_PHONE` / `ADMIN_PASSWORD` | нет | `+79990000000` / `Admin12345` | bootstrap админа |

### Тюнинг

| Переменная | Дефолт | Назначение |
|---|---|---|
| `HTTP_ADDR` | `:8080` | адрес API |
| `METRICS_ADDR` | `:8081` | метрики worker'а |
| `HTTP_REQUEST_TIMEOUT` | `20s` | таймаут обработчика |
| `HTTP_SHUTDOWN_TIMEOUT` | `20s` | graceful shutdown |
| `DB_MAX_CONNS` / `DB_MIN_CONNS` | `20` / `2` | пул pgx |
| `REDIS_CACHE_TTL` | `60s` | TTL карточки номера |
| `JWT_ACCESS_TTL` | `15m` | access token |
| `JWT_REFRESH_TTL` | `720h` | refresh token |
| `ARGON2_MEMORY_KB` | `65536` | 64 MiB |
| `ARGON2_ITERATIONS` | `3` | |
| `RATE_LIMIT_ENABLED` | `true` | |
| `RATE_LIMIT_PUBLIC_RPM` | `120` | публичные запросы/мин/IP |
| `RATE_LIMIT_AUTH_RPM` | `20` | auth-запросы/мин/IP |
| `KAFKA_ENABLED` | `true` | `false` отключает producer/consumer |
| `CORS_ALLOWED_ORIGINS` | `*` | список через запятую |

> В production задайте конкретные origins, а не `*`.

---

## 3. Docker Compose

### Запуск

```bash
docker compose -f deploy/docker/docker-compose.yml up -d --build
docker compose -f deploy/docker/docker-compose.yml ps
```

Поднимаются: `postgres`, `redis`, `kafka`, `kafka-init`, `kafka-ui`, `api`,
`worker`, `prometheus`, `grafana`.

### Проверка

```bash
curl -s http://localhost:8080/healthz
curl -s http://localhost:8080/readyz | jq
curl -s http://localhost:8080/api/v1/numbers/+79991234567 | jq
docker compose -f deploy/docker/docker-compose.yml logs -f api worker
```

### Порты

| Сервис | Порт | URL |
|---|---|---|
| API | 8080 | <http://localhost:8080> |
| Swagger | 8080 | <http://localhost:8080/docs> |
| Worker metrics | 9091 | <http://localhost:9091/metrics> |
| PostgreSQL | 5432 | `postgres://getcontact:getcontact@localhost:5432/getcontact` |
| Redis | 6379 | `redis://localhost:6379` |
| Kafka (external) | 29092 | `localhost:29092` |
| Kafka UI | 8090 | <http://localhost:8090> |
| Prometheus | 9090 | <http://localhost:9090> |
| Grafana | 3000 | <http://localhost:3000> — логин `admin`, пароль `Admin12345` |

> Grafana-пароль задаётся в compose переменными `GF_SECURITY_ADMIN_USER` и
> `GF_SECURITY_ADMIN_PASSWORD`. Он применяется при **первом** создании тома
> `grafanadata`; если том уже существует, Grafana использует сохранённый пароль.
> Сбросить пароль без потери дашбордов:
>
> ```bash
> docker exec gc-grafana grafana cli admin reset-admin-password Admin12345
> ```

### Остановка

```bash
# остановить, сохранить данные
docker compose -f deploy/docker/docker-compose.yml down

# остановить и удалить тома (полный сброс БД/Redis/Kafka)
docker compose -f deploy/docker/docker-compose.yml down -v --remove-orphans
```

### Kafka listener'ы

Compose использует два listener'а:

- `INTERNAL://kafka:9092` — для api/worker внутри сети;
- `EXTERNAL://localhost:29092` — для инструментов с хоста.

Если подключаетесь с хоста, используйте `localhost:29092`, а не `9092`.

---

## 4. Kubernetes

Полный пошаговый гайд — [deploy/k8s/README.md](../deploy/k8s/README.md).
Кратко:

```bash
# 1. Образы
docker build -f deploy/docker/Dockerfile --target api    -t ghcr.io/your-org/getcontact-api:1.1.0    .
docker build -f deploy/docker/Dockerfile --target worker -t ghcr.io/your-org/getcontact-worker:1.1.0 .
docker push ghcr.io/your-org/getcontact-api:1.1.0
docker push ghcr.io/your-org/getcontact-worker:1.1.0

# 2. Namespace + секреты
kubectl apply -f deploy/k8s/00-namespace.yaml
kubectl -n getcontact create secret generic getcontact-secrets \
  --from-literal=POSTGRES_PASSWORD='<strong>' \
  --from-literal=DATABASE_URL='postgres://getcontact:<strong>@postgres:5432/getcontact?sslmode=disable' \
  --from-literal=JWT_SECRET="$(openssl rand -hex 32)" \
  --from-literal=ADMIN_PASSWORD='<strong>' \
  --dry-run=client -o yaml | kubectl apply -f -

# 3. Всё остальное
kubectl apply -k deploy/k8s
kubectl -n getcontact rollout status deploy/api
kubectl -n getcontact port-forward svc/api 8080:80
```

### Что входит

| Манифест | Содержимое |
|---|---|
| `00-namespace.yaml` | namespace `getcontact` |
| `01-config.yaml` | ConfigMap + Secret (шаблон) |
| `02-postgres.yaml` | Service + StatefulSet, PVC 10Gi, probes |
| `03-redis.yaml` | Service + StatefulSet, AOF, maxmemory 256mb |
| `04-kafka.yaml` | KRaft StatefulSet + init Job (топики) |
| `05-api.yaml` | Deployment 2 реплики, Service, HPA 2→10, PDB |
| `06-worker.yaml` | Deployment, Service метрик |
| `07-ingress.yaml` | nginx Ingress + TLS |
| `08-monitoring.yaml` | ServiceMonitor ×2 + PrometheusRule |
| `09-networkpolicy.yaml` | default-deny + точечные разрешения |

### Особенности

- API: `runAsNonRoot`, `readOnlyRootFilesystem`, `drop: [ALL]`,
  startup/readiness/liveness probes, rolling update `maxUnavailable: 0`.
- HPA по CPU 70%, scale-down stabilization 300 с.
- PDB `minAvailable: 1`.
- NetworkPolicy разрешает DNS, ingress от ingress-nginx/monitoring,
  egress api/worker только к Postgres/Redis/Kafka.
- ServiceMonitor'ы помечены `release: kube-prometheus-stack` — измените label,
  если ваш Prometheus Operator использует другой селектор.

### Образы

В манифестах стоит placeholder:

```text
ghcr.io/your-org/getcontact-api:1.1.0
ghcr.io/your-org/getcontact-worker:1.1.0
```

Замените на свой registry. Для приватного registry добавьте `imagePullSecrets`:

```bash
kubectl -n getcontact create secret docker-registry ghcr \
  --docker-server=ghcr.io --docker-username=<user> --docker-password=<token>
kubectl -n getcontact patch serviceaccount default \
  -p '{"imagePullSecrets":[{"name":"ghcr"}]}'
```

---

## 5. Production checklist

### Безопасность

- [ ] `JWT_SECRET` ≥ 32 случайных символов, хранится в secret manager.
- [ ] Пароль Postgres сильный, `DATABASE_URL` использует TLS (`sslmode=require`).
- [ ] `APP_ENV=production` (включает проверку JWT secret).
- [ ] `CORS_ALLOWED_ORIGINS` — конкретные домены.
- [ ] `ADMIN_PASSWORD` сменён; bootstrap-админ не используется как рабочий аккаунт.
- [ ] Ingress с TLS, HSTS на уровне прокси.
- [ ] NetworkPolicy включена, CNI её поддерживает.
- [ ] Образы сканируются (Trivy/Grype), теги иммутабельны (digest).
- [ ] Секреты не в git; `deploy/k8s/01-config.yaml` — только шаблон.

### Надёжность

- [ ] PostgreSQL: managed/HA, PITR-бэкапы, `pg_stat_statements`.
- [ ] Redis: Sentinel/Cluster или managed, `maxmemory-policy allkeys-lru`.
- [ ] Kafka: 3+ брокера, RF=3, `min.insync.replicas=2`, отдельные топики DLQ.
- [ ] API: ≥ 2 реплик, HPA, PDB, topologySpreadConstraints по зонам.
- [ ] Worker: ≥ 1 реплика; consumer group сам балансирует партиции.
- [ ] Probes настроены, `terminationGracePeriodSeconds ≥ shutdown timeout`.
- [ ] Alembic-подобные миграции выполняются под advisory lock (уже реализовано).

### Наблюдаемость

- [ ] Prometheus скрейпит `/metrics` API и worker.
- [ ] Grafana-дашборд импортирован (в k8s — через ConfigMap/Grafana Operator).
- [ ] Алерты из `alerts.yml` / `PrometheusRule` доставлены в Alertmanager.
- [ ] Recording rules из `rules.yml` / `PrometheusRule` загружены (p97).
- [ ] Логи централизованы (Loki/ELK), `request_id` в каждой записи.
- [ ] SLO: p97 < 300 мс, 5xx < 1%, cache hit > 70%.

### Данные

- [ ] Ежедневный бэкап Postgres + проверка restore.
- [ ] Ретеншн `audit_events` и `refresh_tokens` (cron/worker cleanup).
- [ ] Политика хранения спам-репортов и персональных данных (GDPR/152-ФЗ).
- [ ] Мониторинг размера таблиц и bloat.

---

## 6. Масштабирование

| Симптом | Действие |
|---|---|
| Высокий RPS, CPU API > 70% | HPA увеличивает реплики; проверьте p97 |
| Низкий cache hit | увеличьте `REDIS_CACHE_TTL`, проверьте инвалидацию |
| Медленные запросы к БД | `EXPLAIN ANALYZE`, проверьте GIN/trigram, увеличьте пул |
| Много 429 | поднимите `RATE_LIMIT_PUBLIC_RPM` или добавьте Redis-кластер |
| Отставание consumer'а | добавьте партиции/реплики worker'а |
| DLQ растёт | разберите сообщения в Kafka UI, исправьте обработчик, переиграйте |

Postgres и Redis в `deploy/k8s` — single-instance baseline. Для нагрузки
используйте managed-сервисы или операторы (CloudNativePG, Strimzi, Redis Operator).

---

## 7. Обновление и откат

```bash
# Сборка новой версии
docker build -f deploy/docker/Dockerfile --target api -t ghcr.io/your-org/getcontact-api:1.0.1 .
docker push ghcr.io/your-org/getcontact-api:1.0.1

# Обновление
kubectl -n getcontact set image deploy/api api=ghcr.io/your-org/getcontact-api:1.0.1
kubectl -n getcontact rollout status deploy/api

# Откат
kubectl -n getcontact rollout undo deploy/api
```

Миграции применяются при старте API. Перед несовместимой миграцией:
остановите worker, сделайте бэкап, примените миграцию, поднимите worker.

---

## 8. Troubleshooting

| Проблема | Причина | Решение |
|---|---|---|
| `/readyz` 503, `postgres: down` | неверный DSN / БД не готова | проверьте `DATABASE_URL`, логи postgres |
| `redis: down` | неверный адрес/пароль | `redis-cli -h ... ping` |
| `kafka: down` | advertised listener | внутри сети — `kafka:9092`, с хоста — `localhost:29092` |
| API падает на старте `JWT_SECRET must be at least 32` | production + короткий секрет | задайте длинный секрет |
| 401 после рестарта | Redis потерял denylist/сессии | это ожидаемо; пользователи перелогинятся |
| 429 при тестах | лимит auth 20/мин | для demo поднимите `RATE_LIMIT_AUTH_RPM` |
| Контейнер `unhealthy` | нет `wget`/`curl` в образе | используйте target с busybox (уже включён) |
| Kafka UI пуст | топики ещё не созданы | дождитесь `kafka-init`, проверьте логи |
| Grafana без данных | Prometheus не скрейпит | проверьте `up{job="getcontact-api"}` |
| Grafana не пускает с `admin` / `Admin12345` | пароль изменён в томе `grafanadata` | `docker exec gc-grafana grafana cli admin reset-admin-password Admin12345` |
