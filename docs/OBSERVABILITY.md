# Observability

Стек: **Prometheus** (сбор + алерты) → **Grafana** (визуализация).
API отдаёт `/metrics` на `:8080`, worker — на `METRICS_ADDR` (по умолчанию `:9091`).
Метрики регистрируются в default registry и включают `go_*` и `process_*` коллекторы.

---

## 1. Быстрый доступ

| Компонент | URL | Учётные данные |
|---|---|---|
| Prometheus | <http://localhost:9090> | — |
| Grafana | <http://localhost:3000> | `admin` / `Admin12345` |
| API metrics | <http://localhost:8080/metrics> | — |
| Worker metrics | <http://localhost:9091/metrics> | — |
| Swagger | <http://localhost:8080/docs> | — |

> **Grafana в demo-стеке:** логин `admin`, пароль `Admin12345`
> (`GF_SECURITY_ADMIN_USER` / `GF_SECURITY_ADMIN_PASSWORD` в
> `deploy/docker/docker-compose.yml`). Если пароль меняли вручную, сбросьте:
>
> ```bash
> docker exec gc-grafana grafana cli admin reset-admin-password Admin12345
> ```

Проверка целей:

```promql
up{job=~"getcontact-.*"}
```

Ожидаемо три цели: `getcontact-api`, `getcontact-worker`, `prometheus`.

---

## 2. Каталог метрик

### HTTP

| Метрика | Тип | Labels | Смысл |
|---|---|---|---|
| `getcontact_http_requests_total` | counter | `method`, `route`, `status` | все завершённые запросы |
| `getcontact_http_request_duration_seconds` | histogram | `method`, `route` | латентность |
| `getcontact_http_in_flight_requests` | gauge | — | запросы в обработке |
| `getcontact_http_response_size_bytes` | histogram | `method`, `route` | размер ответа |

`route` — шаблон (`/api/v1/numbers/{phone}`), поэтому кардинальность ограничена.

### База данных

| Метрика | Тип | Labels |
|---|---|---|
| `getcontact_db_query_duration_seconds` | histogram | `operation` |

`operation` — логическое имя (`user.get_by_phone`, `number.lookup`, `contact.list`, ...).

### Кэш

| Метрика | Тип | Labels | Значения |
|---|---|---|---|
| `getcontact_cache_operations_total` | counter | `operation`, `result` | `get/set/delete`, `hit/miss/ok/error` |

### Kafka

| Метрика | Тип | Labels | Значения |
|---|---|---|---|
| `getcontact_kafka_events_total` | counter | `direction`, `topic`, `result` | `publish/consume`, `ok/error` |

### Бизнес

| Метрика | Тип | Labels |
|---|---|---|
| `getcontact_business_number_lookups_total` | counter | `result` (`hit`/`miss`/`error`) |
| `getcontact_business_spam_reports_total` | counter | — |
| `getcontact_business_tags_added_total` | counter | — |

### Аутентификация

| Метрика | Тип | Labels |
|---|---|---|
| `getcontact_auth_attempts_total` | counter | `operation` (`register/login/refresh/logout/logout_all/session_revoke`), `result` (`ok/invalid/conflict/denied/blocked`) |

### Сборка и runtime

| Метрика | Тип | Labels |
|---|---|---|
| `getcontact_build_info` | gauge | `version`, `go_version` |
| `go_goroutines`, `go_memstats_*`, `go_gc_*` | — | стандартные |
| `process_*` | — | CPU, RSS, FDs |

---

## 3. Полезные PromQL-запросы

### Трафик и ошибки

```promql
# RPS
sum(rate(getcontact_http_requests_total[5m]))

# RPS по маршрутам
sum by (route) (rate(getcontact_http_requests_total[5m]))

# Доля 5xx
100 * sum(rate(getcontact_http_requests_total{status=~"5.."}[5m]))
    / clamp_min(sum(rate(getcontact_http_requests_total[5m])), 0.001)

# 4xx по кодам
sum by (status) (rate(getcontact_http_requests_total{status=~"4.."}[5m]))
```

### Латентность

```promql
# p50 / p95 / p97 / p99
histogram_quantile(0.50, sum by (le) (rate(getcontact_http_request_duration_seconds_bucket[5m])))
histogram_quantile(0.95, sum by (le) (rate(getcontact_http_request_duration_seconds_bucket[5m])))
histogram_quantile(0.97, sum by (le) (rate(getcontact_http_request_duration_seconds_bucket[5m])))
histogram_quantile(0.99, sum by (le) (rate(getcontact_http_request_duration_seconds_bucket[5m])))

# p97 по маршруту
histogram_quantile(0.97,
  sum by (le, route) (rate(getcontact_http_request_duration_seconds_bucket[5m])))
```

p97 закреплён как основной SLO. Запросы записаны в
`deploy/docker/prometheus/rules.yml` (и в `PrometheusRule` для Kubernetes):

```promql
getcontact:http_request_duration_seconds:p97_5m
getcontact:http_request_duration_seconds:p97_5m:by_route
getcontact:db_query_duration_seconds:p97_5m
```

### Кэш

```promql
# Hit ratio
sum(rate(getcontact_cache_operations_total{operation="get",result="hit"}[15m]))
/
clamp_min(sum(rate(getcontact_cache_operations_total{operation="get"}[15m])), 0.001)

# Ошибки Redis
sum(rate(getcontact_cache_operations_total{result="error"}[5m]))
```

### База данных

```promql
# p97 по операциям
histogram_quantile(0.97,
  sum by (le, operation) (rate(getcontact_db_query_duration_seconds_bucket[5m])))

# Самая медленная операция
topk(5,
  histogram_quantile(0.97,
    sum by (le, operation) (rate(getcontact_db_query_duration_seconds_bucket[5m]))))
```

### Kafka

```promql
# Публикация и потребление
sum by (direction, topic, result) (rate(getcontact_kafka_events_total[5m]))

# Ошибки публикации
increase(getcontact_kafka_events_total{direction="publish",result="error"}[5m])
```

### Бизнес

```promql
# Lookup RPS по результату
sum by (result) (rate(getcontact_business_number_lookups_total[5m]))

# Спам-репорты за сутки
increase(getcontact_business_spam_reports_total[24h])

# Теги за сутки
increase(getcontact_business_tags_added_total[24h])

# Доля успешных логинов
sum(rate(getcontact_auth_attempts_total{operation="login",result="success"}[15m]))
/
clamp_min(sum(rate(getcontact_auth_attempts_total{operation="login"}[15m])), 0.001)
```

---

## 4. Grafana-дашборд

`deploy/docker/grafana/dashboards/getcontact-overview.json`
(uid `getcontact-overview`) провижинится автоматически.

| # | Панель | Тип | Запрос (сокращённо) |
|---|---|---|---|
| 1 | Requests / sec | stat | `sum(rate(...requests_total[5m]))` |
| 2 | 5xx error rate | stat | доля 5xx |
| 3 | p97 latency | stat | `histogram_quantile(0.97, ...)` |
| 4 | In-flight requests | stat | `...in_flight_requests` |
| 5 | Request rate by route | timeseries | `sum by (route)` |
| 6 | Responses by status | timeseries | `sum by (status)` |
| 7 | Latency percentiles | timeseries | p50/p95/p97/p99 |
| 8 | Redis cache hit ratio | stat | hit / get |
| 9 | Caller-ID lookups | timeseries | `sum by (result)` |
| 10 | Database p97 by operation | timeseries | `sum by (operation)` |
| 11 | Kafka events | timeseries | `sum by (direction,topic,result)` |
| 12 | Spam reports (24h) | stat | `increase(...[24h])` |
| 13 | Tags added (24h) | stat | `increase(...[24h])` |
| 14 | Heap in use | timeseries | `go_memstats_heap_inuse_bytes` |
| 15 | Goroutines | timeseries | `go_goroutines` |
| 16 | Route p97 latency | timeseries | recorded `...:p97_5m:by_route` |

Ссылки в шапке дашборда ведут на Swagger UI и Prometheus.

### Переменные и фильтры

Дашборд без шаблонных переменных — рассчитан на один namespace/cluster.
Для мультикластерного окружения добавьте переменную `cluster` и label
`cluster` в `external_labels` Prometheus.

---

## 5. Алерты

Файл `deploy/docker/prometheus/alerts.yml`; в Kubernetes — `PrometheusRule`
в `deploy/k8s/08-monitoring.yaml`.

| Алерт | Условие | For | Severity |
|---|---|---|---|
| `GetContactApiDown` | `up{job="getcontact-api"} == 0` | 2m | critical |
| `GetContactWorkerDown` | `up{job="getcontact-worker"} == 0` | 2m | critical |
| `GetContactHigh5xxRate` | 5xx > 5% | 5m | warning |
| `GetContactHighLatencyP97` | p97 > 1s | 5m | warning |
| `GetContactRouteLatencyP97High` | route p97 > 2s | 10m | warning |
| `GetContactNoTraffic` | RPS == 0 | 10m | info |
| `GetContactCacheHitRateLow` | hit rate < 50% | 15m | warning |
| `GetContactKafkaPublishErrors` | publish errors > 0 | 1m | warning |
| `GetContactDBLatencyHigh` | DB p97 > 500ms | 5m | warning |
| `GetContactSpamReportSurge` | > 500 reports / 10m | 1m | info |
| `GetContactTargetDown` (k8s) | `up{namespace="getcontact"} == 0` | 2m | critical |

### Runbook

**API down**
1. `docker compose ps` / `kubectl -n getcontact get pods`.
2. Логи: `docker compose logs api --tail=200` или `kubectl logs deploy/api`.
3. Проверить `/healthz` и `/readyz`.
4. Частые причины: неверный `DATABASE_URL`, недоступен Redis, миграция упала,
   короткий `JWT_SECRET` в production.

**5xx > 5%**
1. Найти маршрут: `topk(5, sum by (route) (rate(getcontact_http_requests_total{status=~"5.."}[5m])))`.
2. Проверить логи с `request_id`.
3. Проверить `getcontact_db_query_duration_seconds` и `/readyz`.

**p97 > 1s**
1. Разбить по route и operation; посмотреть панель `Route p97 latency`.
2. Проверить cache hit ratio — падение кэша увеличивает нагрузку на БД.
3. Проверить пул соединений (`DB_MAX_CONNS`) и медленные SQL (`pg_stat_statements`).

**Cache hit < 50%**
1. Проверить TTL (`REDIS_CACHE_TTL`).
2. Проверить ошибки Redis: `getcontact_cache_operations_total{result="error"}`.
3. Оценить разнообразие номеров — при равномерном случайном трафике hit rate
   ограничен TTL и рабочим набором.

**Kafka publish errors**
1. `docker compose logs kafka` / `kubectl logs kafka-0`.
2. Проверить `KAFKA_BROKERS`, listener'ы (`kafka:9092` внутри сети).
3. Проверить заполненность диска и `getcontact.events.dlq`.

**DB p97 > 500ms**
1. `topk` по operation.
2. `EXPLAIN (ANALYZE, BUFFERS)` для медленного запроса.
3. Проверить наличие/использование индексов, bloat, autovacuum.
4. При росте нагрузки — увеличить пул/реплики, вынести чтение на replica.

**Spam report surge**
1. Проверить распределение по номерам и репортерам.
2. Уникальный индекс `(phone, reporter_id)` уже ограничивает накрутку одним голосом.
3. При аномалии — временно снизить `RATE_LIMIT_PUBLIC_RPM`, заблокировать аккаунты.

---

## 6. Логи

- Формат: JSON (`slog`), уровень из `APP_ENV` (dev — text).
- Поля: `time`, `level`, `msg`, `request_id`, `method`, `path`, `status`,
  `duration_ms`, `ip`, `user_id` (если аутентифицирован).
- Ошибки 5xx логируются со стеком; 4xx — без стека.
- Корреляция: `X-Request-Id` возвращается клиенту и попадает в тело ошибки.

Пример:

```json
{"time":"2026-01-01T10:00:00Z","level":"INFO","msg":"http request","request_id":"4a8f2c1e9b7d","method":"GET","path":"/api/v1/numbers/+79991234567","status":200,"duration_ms":3.2}
```

### Сбор

- Docker: `docker compose logs -f api worker`.
- Kubernetes: `kubectl -n getcontact logs -l app.kubernetes.io/name=api -f`.
- Production: Promtail/Vector → Loki, или Fluent Bit → Elasticsearch.

---

## 7. SLO и ёмкость

| SLI | Цель | Как считать |
|---|---|---|
| Availability | 99.9% | `1 - 5xx/total` |
| Latency p97 | < 300 ms | histogram_quantile(0.97) |
| Error budget | 0.1% | 5xx за 30 дней |
| Cache hit | > 70% | hit/get |
| Kafka lag | < 1000 | `kafka_consumergroup_lag` (JMX exporter) |

> Для Kafka lag нужен JMX exporter или Kafka Exporter — в демо-стенде не включён.
> Добавьте sidecar `kafka-exporter` и job в `prometheus.yml` при необходимости.

---

## 8. Проверка наблюдаемости

```bash
# 1. Метрики API
curl -s http://localhost:8080/metrics | grep getcontact_http_requests_total | head

# 2. Цели Prometheus
curl -s http://localhost:9090/api/v1/targets | jq '.data.activeTargets[].labels.job'

# 3. Запрос PromQL
curl -s 'http://localhost:9090/api/v1/query?query=sum(rate(getcontact_http_requests_total[5m]))' | jq

# 4. Алерты
curl -s http://localhost:9090/api/v1/rules | jq '.data.groups[].rules[].name'

# 5. Grafana health
curl -s http://localhost:3000/api/health
```

После нескольких запросов к API на дашборде появятся данные в течение 10–15 секунд
(`refresh: 10s`, `scrape_interval: 15s`).
