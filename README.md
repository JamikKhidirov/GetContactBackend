<div align="center">

<img src="docs/assets/banner.svg" alt="GetContact API — animated banner" width="100%" />

# GetContact API

**Production-grade Go backend for caller-ID, community tags, spam intelligence and personal contacts.**

[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?style=for-the-badge&logo=go&logoColor=white)](https://go.dev)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16-4169E1?style=for-the-badge&logo=postgresql&logoColor=white)](https://www.postgresql.org)
[![Redis](https://img.shields.io/badge/Redis-7-DC382D?style=for-the-badge&logo=redis&logoColor=white)](https://redis.io)
[![Kafka](https://img.shields.io/badge/Kafka-KRaft-231F20?style=for-the-badge&logo=apachekafka&logoColor=white)](https://kafka.apache.org)
[![Docker](https://img.shields.io/badge/Docker-Compose-2496ED?style=for-the-badge&logo=docker&logoColor=white)](deploy/docker)
[![Kubernetes](https://img.shields.io/badge/Kubernetes-ready-326CE5?style=for-the-badge&logo=kubernetes&logoColor=white)](deploy/k8s)
[![Tests](https://img.shields.io/badge/tests-unit%20%2B%20integration-34D399?style=for-the-badge&logo=go&logoColor=white)](test)
[![License](https://img.shields.io/badge/license-MIT-818CF8?style=for-the-badge)](LICENSE)

[Quick start](#-быстрый-старт) · [API](#-все-эндпоинты) · [Architecture](docs/ARCHITECTURE.md) · [OpenAPI](api/openapi.yaml) · [Postman](api/postman_collection.json) · [Deploy](docs/DEPLOYMENT.md) · [Observability](docs/OBSERVABILITY.md)

</div>

---

## ✨ Что это

Полноценный бэкенд в стиле **GetContact**: по номеру телефона отдаёт карточку с именем,
оператором, страной, community-тегами и spam score. Пользователи ведут личную телефонную
книгу, жалуются на спам, ищут по номерам и контактам. Администратор видит статистику,
блокирует нарушителей и модерирует очередь репортов.

Всё «от и до»: чистая архитектура, миграции с индексами, JWT-ротация, Redis-кэш и
rate limit, Kafka-события с идемпотентным consumer'ом и DLQ, Prometheus-метрики,
готовый Grafana-дашборд, Docker Compose, Kubernetes-манифесты, Swagger UI, OpenAPI,
Postman, unit- и black-box интеграционные тесты.

<table>
<tr>
<td width="50%">

### 🧠 Домен
- 🔎 Caller-ID lookup с кэшем и счётчиком
- 🏷️ Community-теги с weight и анти-накруткой
- 🚨 Спам-репорты, spam score 0..100
- 📇 Личная телефонная книга + bulk import
- 🔍 Единый поиск по номерам и контактам
- 🛡️ Админка: статистика, блокировки, модерация

</td>
<td width="50%">

### ⚙️ Платформа
- 🧱 Clean Architecture: domain → service → transport
- 🐘 PostgreSQL 16 + pgx, индексы и триггеры
- ⚡ Redis: кэш, fixed-window rate limit, JWT blacklist
- 📨 Kafka: события, consumer group, DLQ, idempotency
- 📈 Prometheus + Grafana + алерты
- 🐳 Docker Compose и ☸️ Kubernetes (HPA, PDB, probes)

</td>
</tr>
</table>

---

## 🚀 Быстрый старт

### 1. Поднять весь стек

```bash
docker compose -f deploy/docker/docker-compose.yml up -d --build
```

Первый запуск собирает два образа (api и worker) и поднимает Postgres, Redis, Kafka,
Prometheus, Grafana и Kafka UI.

### 2. Проверить

```bash
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
curl http://localhost:8080/api/v1/numbers/+79991234567
```

### 3. Открыть

| Что | URL | Логин |
|---|---|---|
| 🧪 Swagger UI | <http://localhost:8080/docs> | — |
| 📘 OpenAPI YAML | <http://localhost:8080/openapi.yaml> | — |
| 📊 Grafana | <http://localhost:3000> | `admin` / `Admin12345` |
| 🔥 Prometheus | <http://localhost:9090> | — |
| 🧾 Kafka UI | <http://localhost:8090> | — |
| ❤️ Health | <http://localhost:8080/healthz> | — |
| 📈 Metrics | <http://localhost:8080/metrics> | — |

### 4. Демо-доступы

| Сервис | Логин | Пароль | Где задаётся |
|---|---|---|---|
| 👑 API-администратор | `+79990000000` | `Admin12345` | `ADMIN_PHONE` / `ADMIN_PASSWORD` в compose |
| 📊 Grafana | `admin` | `Admin12345` | `GF_SECURITY_ADMIN_USER` / `GF_SECURITY_ADMIN_PASSWORD` в compose |

API-администратор создаётся при старте API: телефон повышается до роли `admin`,
а пароль синхронизируется с `ADMIN_PASSWORD` при каждом рестарте.

Grafana хранит пароль в томе `grafanadata`. Если пароль меняли вручную и он
не подходит, сбросьте его одной командой:

```bash
docker exec gc-grafana grafana cli admin reset-admin-password Admin12345
```

> ⚠️ Смените `JWT_SECRET`, `ADMIN_PASSWORD`, `GF_SECURITY_ADMIN_PASSWORD` и пароль
> Postgres перед любым реальным деплоем.

---

## 🧭 Архитектура

```mermaid
flowchart LR
    subgraph Client["Клиенты"]
        M["Mobile / Web"]
        PM["Postman / curl"]
    end

    subgraph Edge["Edge"]
        ING["Ingress / Load Balancer"]
    end

    subgraph API["Go API (stateless)"]
        MW["Middleware<br/>JWT · Rate limit · Metrics · CORS"]
        H["HTTP handlers"]
        S["Services<br/>auth · contacts · numbers · admin"]
        R["Repositories<br/>pgx · redis · kafka"]
    end

    subgraph Worker["Go Worker"]
        C["Kafka consumer<br/>idempotent + DLQ"]
        W["Derived data<br/>spam score · audit"]
    end

    subgraph Data["Stateful"]
        PG[("PostgreSQL 16")]
        RD[("Redis 7")]
        KF[("Kafka KRaft")]
    end

    subgraph Obs["Observability"]
        PR["Prometheus"]
        GF["Grafana"]
    end

    M --> ING --> MW --> H --> S --> R
    PM --> ING
    R --> PG
    R --> RD
    R --> KF
    KF --> C --> W --> PG
    R -. "/metrics" .-> PR
    C -. "/metrics" .-> PR
    PR --> GF
```

Подробный разбор слоёв, sequence-диаграммы и схема БД — в [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

### Слои

```text
cmd/api, cmd/worker          ← composition root, только wiring
internal/transport/http      ← роутер, middleware, DTO, коды ошибок
internal/service             ← бизнес-сценарии, валидация, права
internal/domain              ← сущности и интерфейсы-порты (без фреймворков)
internal/repository/...      ← pgx, Redis, Kafka-адаптеры
internal/platform            ← телефон, argon2id, JWT, slog
internal/metrics             ← все Prometheus-коллекторы
migrations                   ← SQL + embed, advisory-lock миграции
```

Зависимости направлены **внутрь**: `transport → service → domain`, а БД и Kafka
подключаются через интерфейсы. Домен ничего не знает про HTTP, SQL и Kafka.

---

## 🔐 Аутентификация

```mermaid
sequenceDiagram
    autonumber
    participant C as Client
    participant A as API
    participant R as Redis
    participant P as PostgreSQL

    C->>A: POST /auth/login {phone, password}
    A->>P: SELECT user by phone
    A->>A: argon2id verify
    A->>P: INSERT refresh_token (SHA-256 hash)
    A-->>C: access JWT (15m) + refresh (30d, single-use)
    C->>A: GET /auth/me (Bearer access)
    A->>R: EXISTS auth:deny:{jti}
    A-->>C: 200 profile
    C->>A: POST /auth/refresh {refresh_token}
    A->>P: revoke old + issue new pair (rotation)
    C->>A: POST /auth/logout
    A->>R: SET auth:deny:{jti} TTL=remaining
    A->>P: revoke refresh
```

- **Пароли** — argon2id (64 MiB, t=3, p=2), PHC-формат.
- **Access JWT** — HS256, 15 минут, `sub` + `role` + `jti`.
- **Refresh** — 32 случайных байта, в БД только SHA-256, ротация при каждом refresh.
- **Logout** — refresh отзывается, access JTI попадает в Redis blacklist до истечения.
- **Блокировка** — отзывает все refresh-сессии пользователя.
- **Rate limit** — Redis fixed window: auth 20/мин, public/user 120/мин на IP.
  При недоступности Redis лимитер **fail-open**, чтобы кэш не ронял API.

---

## 🧩 Все эндпоинты

Полная спецификация: [api/openapi.yaml](api/openapi.yaml) · Postman: [api/postman_collection.json](api/postman_collection.json) · Подробные примеры: [docs/API.md](docs/API.md).

Легенда: 🔓 публичный · 🔑 Bearer · 👑 admin.

### Health & Ops

| Метод | Путь | Доступ | Описание |
|---|---|---|---|
| `GET` | `/` | 🔓 | Индекс сервиса и ссылки |
| `GET` | `/healthz` | 🔓 | Liveness probe |
| `GET` | `/readyz` | 🔓 | Readiness: Postgres, Redis, Kafka |
| `GET` | `/version` | 🔓 | Версия и окружение |
| `GET` | `/metrics` | 🔓 | Prometheus metrics |
| `GET` | `/openapi.yaml` | 🔓 | OpenAPI 3.0.3 |
| `GET` | `/docs` | 🔓 | Swagger UI |

### Auth

| Метод | Путь | Доступ | Описание |
|---|---|---|---|
| `POST` | `/api/v1/auth/register` | 🔓 | Регистрация + выдача токенов |
| `POST` | `/api/v1/auth/login` | 🔓 | Вход по телефону/паролю |
| `POST` | `/api/v1/auth/refresh` | 🔓 | Ротация refresh-токена |
| `POST` | `/api/v1/auth/logout` | 🔑 | Отзыв refresh + blacklist access |
| `GET` | `/api/v1/auth/me` | 🔑 | Текущий профиль |

### Users

| Метод | Путь | Доступ | Описание |
|---|---|---|---|
| `GET` | `/api/v1/users/{id}` | 🔑 | Публичный профиль (телефон маскируется) |
| `PATCH` | `/api/v1/users/me` | 🔑 | Обновить имя/email |
| `PUT` | `/api/v1/users/me/password` | 🔑 | Сменить пароль, отозвать сессии |
| `DELETE` | `/api/v1/users/me` | 🔑 | Удалить аккаунт каскадно |

### Contacts

| Метод | Путь | Доступ | Описание |
|---|---|---|---|
| `GET` | `/api/v1/contacts` | 🔑 | Список: `q`, `favorite`, `page`, `per_page` |
| `POST` | `/api/v1/contacts` | 🔑 | Создать контакт |
| `POST` | `/api/v1/contacts/import` | 🔑 | Bulk upsert до 500 контактов |
| `GET` | `/api/v1/contacts/{id}` | 🔑 | Получить контакт |
| `PATCH` | `/api/v1/contacts/{id}` | 🔑 | Частично обновить |
| `DELETE` | `/api/v1/contacts/{id}` | 🔑 | Удалить |

### Numbers

| Метод | Путь | Доступ | Описание |
|---|---|---|---|
| `GET` | `/api/v1/numbers/{phone}` | 🔓+ | Caller-ID карточка + теги |
| `GET` | `/api/v1/numbers/{phone}/tags` | 🔓 | Теги номера |
| `POST` | `/api/v1/numbers/{phone}/tags` | 🔑 | Добавить/усилить тег |
| `DELETE` | `/api/v1/numbers/{phone}/tags/{tagID}` | 🔑 | Удалить тег (автор или 👑) |
| `POST` | `/api/v1/numbers/{phone}/spam` | 🔑 | Спам-репорт (1 на пользователя) |
| `GET` | `/api/v1/numbers/spam/top` | 🔓 | Топ спам-номеров |
| `GET` | `/api/v1/tags` | 🔓 | Каталог популярных тегов |

### Search & Admin

| Метод | Путь | Доступ | Описание |
|---|---|---|---|
| `GET` | `/api/v1/search` | 🔑 | Поиск по номерам и своим контактам |
| `GET` | `/api/v1/admin/stats` | 👑 | Сводная статистика |
| `GET` | `/api/v1/admin/users` | 👑 | Пользователи с пагинацией |
| `GET` | `/api/v1/admin/users/{id}` | 👑 | Пользователь по ID |
| `PATCH` | `/api/v1/admin/users/{id}/block` | 👑 | Блокировка/разблокировка |
| `DELETE` | `/api/v1/admin/users/{id}` | 👑 | Удалить пользователя |
| `GET` | `/api/v1/admin/reports` | 👑 | Очередь спам-репортов |

### Примеры

```bash
# Регистрация
curl -s -X POST http://localhost:8080/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"phone":"+79991234567","password":"Passw0rd123","display_name":"Ivan"}'

# Caller-ID
curl -s http://localhost:8080/api/v1/numbers/+79991234567

# С токеном
curl -s http://localhost:8080/api/v1/auth/me \
  -H "Authorization: Bearer $ACCESS_TOKEN"

# Спам-репорт
curl -s -X POST http://localhost:8080/api/v1/numbers/+79995555555/spam \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"category":"telemarketing","comment":"Calls every day"}'
```

---

## 🗄️ Данные и оптимизация

```mermaid
erDiagram
    users ||--o{ contacts : owns
    users ||--o{ refresh_tokens : has
    users ||--o{ spam_reports : writes
    users ||--o{ tags : creates
    numbers ||--o{ tags : labeled
    numbers ||--o{ spam_reports : reported

    users {
        uuid id PK
        varchar phone UK
        text password_hash
        varchar role
        bool is_blocked
    }
    contacts {
        uuid id PK
        uuid owner_id FK
        varchar phone
        varchar name
        bool is_favorite
    }
    numbers {
        varchar phone PK
        smallint spam_score
        bigint reports_count
        bigint lookups_count
    }
    tags {
        uuid id PK
        varchar phone FK
        varchar tag
        int weight
    }
    spam_reports {
        uuid id PK
        varchar phone FK
        uuid reporter_id FK
        varchar category
    }
    refresh_tokens {
        uuid id PK
        char token_hash UK
        timestamptz expires_at
    }
```

**Индексы под реальные запросы:**

| Таблица | Индексы |
|---|---|
| `users` | `phone` (unique), `lower(email)` (partial unique), `created_at DESC`, `is_blocked` (partial), GIN trgm по `display_name` |
| `contacts` | `(owner_id, created_at DESC)`, `(owner_id, lower(name))`, `(owner_id)` partial favorite, GIN trgm по `name`, unique `(owner_id, phone)` |
| `numbers` | `(spam_score DESC, reports_count DESC)`, `lookups_count DESC`, GIN trgm по `name` |
| `tags` | unique `(phone, tag)`, `(phone, weight DESC)`, `tag`, `created_by` partial |
| `spam_reports` | `(phone, created_at DESC)`, `created_at DESC`, `reporter_id`, unique `(phone, reporter_id)` |
| `refresh_tokens` | unique `token_hash`, `(user_id)` и `(expires_at)` partial `revoked_at IS NULL` |
| `audit_events` | PK event id, `(event_type, consumed_at DESC)`, `aggregate_id` |

Дополнительно:

- **Spam score** пересчитывается детерминированно: `reports × 12 + risky tag weight × 3`
  (вклад тегов ограничен 40), итог ограничен 100.
- **Один репорт на пользователя и номер** защищает рейтинг от накрутки.
- **Кэш номера** в Redis на 60 секунд; инвалидация при теге/репорте.
- **Lookup count** обновляется атомарным `UPDATE ... + 1` (без read-modify-write).
- **Bulk import** — один `pgx.Batch` на 500 строк вместо 500 round-trip.
- **pgx pool** — 20–25 соединений, health check 30s, ограничение времени жизни.
- **Миграции** идут под `pg_advisory_lock`, поэтому безопасны при N репликах.

---

## 📈 Наблюдаемость

Метрики доступны на `/metrics` (API) и `:9091/metrics` (worker).

| Группа | Метрики |
|---|---|
| HTTP | `getcontact_http_requests_total`, `_request_duration_seconds`, `_in_flight_requests`, `_response_size_bytes` |
| DB | `getcontact_db_query_duration_seconds{operation}` |
| Cache | `getcontact_cache_operations_total{operation,result}` |
| Kafka | `getcontact_kafka_events_total{direction,topic,result}` |
| Business | `getcontact_business_number_lookups_total{result}`, `_spam_reports_total`, `_tags_added_total` |
| Auth | `getcontact_auth_attempts_total{operation,result}` |
| Runtime | `go_*`, `process_*`, `getcontact_build_info` |

Grafana-дашборд **GetContact — API & Platform Overview** (15 панелей) провижинится
автоматически: RPS, 5xx, p50/p95/p99, in-flight, cache hit ratio, lookups, DB latency,
Kafka events, спам-репорты, теги, heap, goroutines.

Алерты: API/worker down, 5xx > 5%, p95 > 1s, cache hit < 50%, Kafka publish errors,
DB p95 > 500ms, отсутствие трафика, всплеск репортов.

Подробнее: [docs/OBSERVABILITY.md](docs/OBSERVABILITY.md).

---

## 📨 Kafka

Топики: `getcontact.events` (3 партиции) и `getcontact.events.dlq`.

Типы событий:

- `user.registered`
- `user.blocked`
- `contact.created`
- `number.looked_up`
- `number.tag_added`
- `number.spam_reported`

Событие — JSON-конверт с `id`, `type`, `version`, `occurred_at`, `aggregate_id`, `payload`.
Worker пишет его в `audit_events` c `ON CONFLICT (id) DO NOTHING` (идемпотентность),
пересчитывает spam score и обновляет агрегаты. Ошибка обработки → DLQ, offset
коммитится только после обработки. Подробнее: [docs/EVENTS.md](docs/EVENTS.md).

---

## 🧪 Тесты

```bash
# Unit: платформа, JWT, телефоны, роутер, middleware
go test ./test/unit/... -v

# Все пакеты
go test ./...

# Black-box интеграционные тесты против поднятого Docker-стека
docker compose -f deploy/docker/docker-compose.yml up -d --build
API_URL=http://localhost:8080 go test ./test/integration -v
```

PowerShell:

```powershell
$env:API_URL = "http://localhost:8080"
go test ./test/integration -v
```

Интеграционный тест проходит полный сценарий: register → login → profile → CRUD контактов →
import → lookup → tag → spam → duplicate spam 409 → search → refresh rotation →
повторный refresh 401 → logout → me 401 → admin login → stats → block → blocked login 403 →
unblock, плюс проверка `/metrics` и `/openapi.yaml`.

---

## 📁 Структура

```text
.
├── api/                      # OpenAPI, Swagger UI, Postman, embed
├── cmd/
│   ├── api/                  # HTTP entrypoint
│   └── worker/               # Kafka consumer entrypoint
├── internal/
│   ├── apperr/               # ошибки → HTTP
│   ├── config/               # env-конфиг
│   ├── domain/               # сущности и порты
│   ├── events/               # Kafka producer/consumer, payloads
│   ├── metrics/              # Prometheus collectors
│   ├── platform/             # phone, argon2id, JWT, slog, validation
│   ├── repository/
│   │   ├── postgres/         # pgx repos + миграции
│   │   └── redis/            # cache, rate limit, denylist
│   ├── service/              # бизнес-логика
│   └── transport/http/       # chi router, middleware, handlers
├── migrations/               # SQL up/down + embed
├── test/
│   ├── unit/                 # unit-тесты
│   └── integration/          # black-box HTTP e2e
├── deploy/
│   ├── docker/               # Dockerfile, compose, Prometheus, Grafana
│   └── k8s/                  # Namespace, Deployments, HPA, PDB, NetworkPolicy
├── docs/                     # архитектура, API, деплой, события, мониторинг
├── scripts/                  # генератор Postman
├── Makefile
└── go.mod
```

---

## ☸️ Kubernetes

```bash
# 1. Соберите и запушьте образы (замените registry)
docker build -f deploy/docker/Dockerfile --target api    -t ghcr.io/your-org/getcontact-api:1.0.0    .
docker build -f deploy/docker/Dockerfile --target worker -t ghcr.io/your-org/getcontact-worker:1.0.0 .
docker push ghcr.io/your-org/getcontact-api:1.0.0
docker push ghcr.io/your-org/getcontact-worker:1.0.0

# 2. Секреты и деплой
kubectl apply -f deploy/k8s/00-namespace.yaml
kubectl -n getcontact create secret generic getcontact-secrets \
  --from-literal=POSTGRES_PASSWORD='...' \
  --from-literal=DATABASE_URL='postgres://getcontact:...@postgres:5432/getcontact?sslmode=disable' \
  --from-literal=JWT_SECRET='...' \
  --from-literal=ADMIN_PASSWORD='...' --dry-run=client -o yaml | kubectl apply -f -

kubectl apply -k deploy/k8s
kubectl -n getcontact rollout status deploy/api
kubectl -n getcontact port-forward svc/api 8080:80
```

В комплекте: 2 реплики API, rolling update `maxUnavailable: 0`, HPA 2→10 по CPU,
PDB, startup/readiness/liveness probes, read-only root filesystem, non-root,
NetworkPolicy и ServiceMonitor/PrometheusRule для kube-prometheus-stack.

> Для production используйте managed PostgreSQL/Redis/Kafka или операторы
> (CloudNativePG, Strimzi, Redis Operator); StatefulSet'ы в `deploy/k8s` — dev/staging baseline.

Полный гайд: [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md).

---

## 🛠️ Make

```bash
make run            # API локально
make worker         # worker локально
make test           # все тесты
make test-e2e       # интеграционные против localhost:8080
make docker-up      # docker compose up --build
make docker-down    # остановить
make docker-logs    # логи api/worker
make fmt vet        # форматирование и vet
make postman        # перегенерировать Postman
make k8s-apply      # kubectl apply -k deploy/k8s
```

---

## 📚 Документация

| Документ | О чём |
|---|---|
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Слои, sequence-диаграммы, модель данных, решения |
| [docs/API.md](docs/API.md) | Все эндпоинты, тела, коды, curl-примеры |
| [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) | Docker, Kubernetes, env, production checklist |
| [docs/OBSERVABILITY.md](docs/OBSERVABILITY.md) | Метрики, PromQL, панели, алерты |
| [docs/EVENTS.md](docs/EVENTS.md) | Kafka-контракты, идемпотентность, DLQ |
| [deploy/k8s/README.md](deploy/k8s/README.md) | Пошаговый Kubernetes-деплой |
| [api/openapi.yaml](api/openapi.yaml) | Машиночитаемая спецификация |
| [api/postman_collection.json](api/postman_collection.json) | 40+ готовых запросов |

---

## ⚖️ License

MIT — см. [LICENSE](LICENSE).

<div align="center">

**Сделано с ❤️ на Go.**

<img src="docs/assets/banner.svg" alt="" width="60%" />

</div>
