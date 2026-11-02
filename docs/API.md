# API Reference

Базовая точка входа: `http://localhost:8080` (Docker Compose).
Машиночитаемая спецификация: [`api/openapi.yaml`](../api/openapi.yaml) · Swagger UI: `/docs`.

**Легенда доступа:** 🔓 публичный · 🔑 Bearer access token · 👑 роль `admin`.

---

## Соглашения

### Успешный ответ

```json
{ "data": { } }
```

Списки дополнительно содержат `meta`:

```json
{
  "data": [],
  "meta": { "page": 1, "per_page": 20, "total": 42, "total_pages": 3 }
}
```

### Ошибка

```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "request validation failed",
    "details": { "phone": "must be a valid phone number (8..15 digits)" },
    "request_id": "4a8f2c1e9b7d"
  }
}
```

| HTTP | Код | Когда |
|---|---|---|
| 400 | `BAD_REQUEST` | битый JSON, неверный UUID/параметр |
| 401 | `UNAUTHORIZED` | нет/просрочен/отозван токен |
| 403 | `FORBIDDEN` | не хватает роли |
| 403 | `USER_BLOCKED` | пользователь заблокирован |
| 404 | `NOT_FOUND` | ресурс не найден |
| 409 | `CONFLICT` | дубликат пользователя/контакта/репорта |
| 422 | `VALIDATION_ERROR` | поля не прошли валидацию |
| 429 | `RATE_LIMITED` | превышен лимит |
| 500 | `INTERNAL_ERROR` | внутренняя ошибка |
| 504 | `REQUEST_TIMEOUT` | обработка превысила `HTTP_REQUEST_TIMEOUT` |

### Заголовки

| Заголовок | Направление | Назначение |
|---|---|---|
| `Authorization: Bearer <access>` | → | аутентификация |
| `X-Request-Id` | ↔ | корреляция; если не передан — генерируется |
| `X-RateLimit-Limit` | ← | лимит окна |
| `X-RateLimit-Remaining` | ← | остаток |
| `Retry-After` | ← | секунды до сброса (429) |

---

## Health & Ops

### `GET /` 🔓

Индекс сервиса: имя, версия, ссылки на docs/openapi/health/metrics.

```bash
curl -s http://localhost:8080/
```

### `GET /healthz` 🔓

Liveness. Не трогает зависимости.

```json
{ "status": "ok", "version": "1.0.0" }
```

### `GET /readyz` 🔓

Readiness. Проверяет Postgres, Redis и (если включён) Kafka.
`200` — все `up`, `503` — хотя бы одна зависимость `down`.

```bash
curl -s -i http://localhost:8080/readyz
```

### `GET /version` 🔓

Версия, имя сервиса, окружение, время сервера.

### `GET /metrics` 🔓

Prometheus exposition format. Скрейпится Prometheus каждые 15 с.

### `GET /openapi.yaml` 🔓

Отдаёт встроенную OpenAPI 3.0.3 спецификацию.

### `GET /docs` 🔓

Swagger UI (ассеты встроены в бинарник, без внешнего CDN), авторизация Bearer через кнопку **Authorize**.

---

## Auth

### `POST /api/v1/auth/register` 🔓

Создаёт пользователя и сразу выдаёт пару токенов.

| Поле | Тип | Обязательно | Правила |
|---|---|---|---|
| `phone` | string | да | 8..15 цифр, нормализуется в `+...` |
| `password` | string | да | 8..128, минимум одна буква и одна цифра |
| `display_name` | string | да | 2..64 символа |
| `email` | string | нет | валидный email, ≤254 |

```bash
curl -s -X POST http://localhost:8080/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{
    "phone": "+79991234567",
    "password": "Passw0rd123",
    "display_name": "Ivan Petrov",
    "email": "ivan@example.com"
  }'
```

`201 Created`:

```json
{
  "data": {
    "user": {
      "id": "0c3f7f1e-2f4e-4c1e-9a51-2f9b7c1d8e44",
      "phone": "+79991234567",
      "email": "ivan@example.com",
      "display_name": "Ivan Petrov",
      "role": "user",
      "is_blocked": false,
      "created_at": "2026-01-01T10:00:00Z",
      "updated_at": "2026-01-01T10:00:00Z"
    },
    "tokens": {
      "access_token": "eyJhbGciOiJIUzI1NiIs...",
      "refresh_token": "h5K...",
      "token_type": "Bearer",
      "expires_in": 900,
      "expires_at": "2026-01-01T10:15:00Z"
    }
  }
}
```

Ошибки: `409` телефон/email занят, `422` валидация, `429` лимит.

### `POST /api/v1/auth/login` 🔓

```bash
curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"phone":"+79991234567","password":"Passw0rd123"}'
```

`200` — та же структура, что у register. `401` — неверные данные,
`403 USER_BLOCKED` — аккаунт заблокирован, `429` — лимит.

### `POST /api/v1/auth/refresh` 🔓

Обменивает refresh на новую пару. Старый refresh становится недействительным.

```bash
curl -s -X POST http://localhost:8080/api/v1/auth/refresh \
  -H 'Content-Type: application/json' \
  -d '{"refresh_token":"<refresh>"}'
```

`200` — новая пара. Повторное использование старого → `401 UNAUTHORIZED`.

### `POST /api/v1/auth/logout` 🔑

Отзывает refresh (если передан) и заносит текущий access JTI в Redis blacklist.

```bash
curl -s -X POST http://localhost:8080/api/v1/auth/logout \
  -H "Authorization: Bearer $ACCESS" \
  -H 'Content-Type: application/json' \
  -d '{"refresh_token":"<refresh>"}'
```

`204 No Content`.

### `GET /api/v1/auth/me` 🔑

```bash
curl -s http://localhost:8080/api/v1/auth/me -H "Authorization: Bearer $ACCESS"
```

`200` — профиль; `401` — токен невалиден/отозван; `403` — заблокирован.

---

## Users

### `GET /api/v1/users/{id}` 🔑

Публичный профиль. Если это не текущий пользователь, телефон маскируется
(`*******4567`), email скрывается.

```bash
curl -s http://localhost:8080/api/v1/users/$USER_ID -H "Authorization: Bearer $ACCESS"
```

### `PATCH /api/v1/users/me` 🔑

| Поле | Тип | Правила |
|---|---|---|
| `display_name` | string | 2..64 |
| `email` | string | email; пустая строка очищает |

```bash
curl -s -X PATCH http://localhost:8080/api/v1/users/me \
  -H "Authorization: Bearer $ACCESS" \
  -H 'Content-Type: application/json' \
  -d '{"display_name":"Ivan Updated","email":"ivan.updated@example.com"}'
```

### `PUT /api/v1/users/me/password` 🔑

```json
{ "current_password": "Passw0rd123", "new_password": "NewPassw0rd456" }
```

`204` — пароль изменён, все refresh-сессии отозваны. `401` — неверный текущий пароль.

### `DELETE /api/v1/users/me` 🔑

Каскадно удаляет контакты, токены и репорты. `204 No Content`.

---

## Contacts

Все операции ограничены владельцем из JWT.

### `GET /api/v1/contacts` 🔑

| Query | Тип | По умолчанию | Описание |
|---|---|---|---|
| `q` | string | — | поиск по имени/номеру (2..64) |
| `favorite` | bool | — | только избранные |
| `page` | int | 1 | страница |
| `per_page` | int | 20 | 1..100 |

```bash
curl -s "http://localhost:8080/api/v1/contacts?page=1&per_page=20&favorite=true" \
  -H "Authorization: Bearer $ACCESS"
```

### `POST /api/v1/contacts` 🔑

| Поле | Тип | Обязательно | Правила |
|---|---|---|---|
| `phone` | string | да | нормализуется |
| `name` | string | да | 1..128 |
| `email` | string | нет | email |
| `note` | string | нет | ≤1000 |
| `is_favorite` | bool | нет | по умолчанию `false` |

```bash
curl -s -X POST http://localhost:8080/api/v1/contacts \
  -H "Authorization: Bearer $ACCESS" \
  -H 'Content-Type: application/json' \
  -d '{"phone":"+79997654321","name":"Alice Johnson","email":"alice@example.com","note":"met at conference","is_favorite":true}'
```

`201`; `409` контакт с таким номером уже есть; `422` валидация.

### `POST /api/v1/contacts/import` 🔑

До 500 контактов за запрос, upsert по `(owner, phone)`.

```bash
curl -s -X POST http://localhost:8080/api/v1/contacts/import \
  -H "Authorization: Bearer $ACCESS" \
  -H 'Content-Type: application/json' \
  -d '{
    "contacts": [
      {"phone":"+79990000001","name":"Bob Stone"},
      {"phone":"+79990000002","name":"Carol White","is_favorite":false}
    ]
  }'
```

`200 { "data": { "imported": 2, "skipped": 0 } }`.

### `GET /api/v1/contacts/{id}` 🔑

`200` контакт; `404` если не найден или принадлежит другому пользователю.

### `PATCH /api/v1/contacts/{id}` 🔑

Любое подмножество: `phone`, `name`, `email`, `note`, `is_favorite`.

```bash
curl -s -X PATCH http://localhost:8080/api/v1/contacts/$CONTACT_ID \
  -H "Authorization: Bearer $ACCESS" \
  -H 'Content-Type: application/json' \
  -d '{"note":"updated note","is_favorite":false}'
```

### `DELETE /api/v1/contacts/{id}` 🔑

`204`; `404` если не найден.

---

## Numbers (caller-ID)

### `GET /api/v1/numbers/{phone}` 🔓+

Публичный lookup. Токен опционален. Неизвестный номер **создаёт пустой профиль**
и возвращает `200`, чтобы клиент мог сразу добавить тег или репорт.

```bash
curl -s http://localhost:8080/api/v1/numbers/+79991234567
```

```json
{
  "data": {
    "number": {
      "phone": "+79991234567",
      "name": "Ivan Petrov",
      "carrier": "MTS",
      "country": "RU",
      "spam_score": 0,
      "reports_count": 0,
      "lookups_count": 12,
      "updated_at": "2026-01-01T10:05:00Z"
    },
    "tags": [
      { "id": "...", "phone": "+79991234567", "tag": "delivery", "weight": 3, "created_by": "...", "created_at": "..." }
    ]
  }
}
```

`422` — некорректный телефон, `429` — лимит. Карточка кэшируется на 60 с.

### `GET /api/v1/numbers/{phone}/tags` 🔓

Список тегов номера, отсортированный по весу.

### `POST /api/v1/numbers/{phone}/tags` 🔑

Добавляет тег. Повторный тег тем же или другим пользователем увеличивает `weight`.
После изменения пересчитывается spam score и инвалидируется кэш.

```bash
curl -s -X POST http://localhost:8080/api/v1/numbers/+79991234567/tags \
  -H "Authorization: Bearer $ACCESS" \
  -H 'Content-Type: application/json' \
  -d '{"tag":"telemarketing"}'
```

`201` — тег; правила: 2..32 символа, буквы/цифры/пробел/`-`/`_`, нижний регистр.

### `DELETE /api/v1/numbers/{phone}/tags/{tagID}` 🔑

Доступно автору тега или администратору. `204`; `403` — чужой тег; `404` — нет тега.

### `POST /api/v1/numbers/{phone}/spam` 🔑

Один активный репорт на пользователя и номер.

| Поле | Тип | Обязательно | Значения |
|---|---|---|---|
| `category` | string | да | `fraud`, `scam`, `telemarketing`, `robocall`, `spam`, `other` |
| `comment` | string | нет | ≤500 |

```bash
curl -s -X POST http://localhost:8080/api/v1/numbers/+79995555555/spam \
  -H "Authorization: Bearer $ACCESS" \
  -H 'Content-Type: application/json' \
  -d '{"category":"telemarketing","comment":"Calls every day"}'
```

`201` — обновлённая карточка (`number` + `tags`) с новым `spam_score`.
Повторный репорт → `409 CONFLICT`.

### `GET /api/v1/numbers/spam/top` 🔓

| Query | По умолчанию | Максимум |
|---|---|---|
| `limit` | 20 | 100 |

```bash
curl -s "http://localhost:8080/api/v1/numbers/spam/top?limit=10"
```

### `GET /api/v1/tags` 🔓

Каталог популярных тегов: `[{ "tag": "spam", "count": 42 }]`.

---

## Search

### `GET /api/v1/search` 🔑

| Query | Обязательно | Правила |
|---|---|---|
| `q` | да | 2..64 |
| `limit` | нет | 1..100, по умолчанию 20 |

```bash
curl -s "http://localhost:8080/api/v1/search?q=Alice&limit=10" \
  -H "Authorization: Bearer $ACCESS"
```

```json
{
  "data": {
    "query": "Alice",
    "numbers": [ { "phone": "+79997654321", "name": "Alice Johnson", "spam_score": 0, "...": "..." } ],
    "contacts": [ { "id": "...", "phone": "+79997654321", "name": "Alice Johnson", "...": "..." } ]
  }
}
```

---

## Admin 👑

Все маршруты требуют `role=admin`; иначе `403 FORBIDDEN`.

### `GET /api/v1/admin/stats`

```json
{
  "data": {
    "users": 128,
    "blocked_users": 2,
    "contacts": 940,
    "numbers": 5120,
    "lookups": 88012,
    "spam_reports": 317,
    "tags": 2044,
    "top_tags": [ { "tag": "spam", "count": 120 } ],
    "top_spam_numbers": [ { "phone": "+79995555555", "spam_score": 96, "...": "..." } ],
    "generated_at": "2026-01-01T10:10:00Z"
  }
}
```

### `GET /api/v1/admin/users`

Query: `q`, `page`, `per_page`. Возвращает страницу пользователей и `meta`.

### `GET /api/v1/admin/users/{id}`

`200` пользователь; `404` не найден.

### `PATCH /api/v1/admin/users/{id}/block`

```bash
curl -s -X PATCH http://localhost:8080/api/v1/admin/users/$USER_ID/block \
  -H "Authorization: Bearer $ADMIN_ACCESS" \
  -H 'Content-Type: application/json' \
  -d '{"blocked":true}'
```

Блокировка отзывает все refresh-сессии. `400` — попытка заблокировать себя.

### `DELETE /api/v1/admin/users/{id}`

Каскадное удаление. `400` — попытка удалить себя; `404` — нет пользователя.

### `GET /api/v1/admin/reports`

Очередь спам-репортов с пагинацией: `page`, `per_page`.

```json
{
  "data": [
    {
      "id": "...",
      "phone": "+79995555555",
      "reporter_id": "...",
      "category": "telemarketing",
      "comment": "Calls every day",
      "created_at": "2026-01-01T10:00:00Z"
    }
  ],
  "meta": { "page": 1, "per_page": 20, "total": 1, "total_pages": 1 }
}
```

---

## Сквозной сценарий (curl)

```bash
BASE=http://localhost:8080

# 1. Регистрация
REG=$(curl -s -X POST $BASE/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"phone":"+79991230001","password":"Passw0rd123","display_name":"Demo User"}')
ACCESS=$(echo "$REG" | python -c 'import sys,json;print(json.load(sys.stdin)["data"]["tokens"]["access_token"])')

# 2. Контакт
curl -s -X POST $BASE/api/v1/contacts \
  -H "Authorization: Bearer $ACCESS" -H 'Content-Type: application/json' \
  -d '{"phone":"+79997650001","name":"Alice","is_favorite":true}'

# 3. Lookup + тег + репорт
curl -s "$BASE/api/v1/numbers/+79995550001"
curl -s -X POST $BASE/api/v1/numbers/+79995550001/tags \
  -H "Authorization: Bearer $ACCESS" -H 'Content-Type: application/json' \
  -d '{"tag":"telemarketing"}'
curl -s -X POST $BASE/api/v1/numbers/+79995550001/spam \
  -H "Authorization: Bearer $ACCESS" -H 'Content-Type: application/json' \
  -d '{"category":"telemarketing"}'

# 4. Поиск
curl -s "$BASE/api/v1/search?q=Alice" -H "Authorization: Bearer $ACCESS"

# 5. Админ
ADMIN=$(curl -s -X POST $BASE/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"phone":"+79990000000","password":"Admin12345"}')
ADMIN_ACCESS=$(echo "$ADMIN" | python -c 'import sys,json;print(json.load(sys.stdin)["data"]["tokens"]["access_token"])')
curl -s $BASE/api/v1/admin/stats -H "Authorization: Bearer $ADMIN_ACCESS"
```

Полный набор готовых запросов — в [Postman-коллекции](../api/postman_collection.json)
(8 папок, авто-сохранение токенов и ID).
