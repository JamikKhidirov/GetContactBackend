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
{ "status": "ok", "version": "1.2.0" }
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
| `email` | string | нет | валидный email, ≤254; нормализуется в lower-case |
| `email_code` | string | нет | код из письма `signup`; создаёт уже подтверждённый адрес |

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
      "email_verified": true,
      "email_verified_at": "2026-01-01T10:00:00Z",
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

### `POST /api/v1/auth/email/code` 🔓

Запрашивает одноразовый код. Назначения: `signup` (подтверждение почты при
регистрации), `login` (вход без пароля), `verify_email` (для авторизованной
сессии, требует `Bearer`). Ответ не раскрывает, существует ли аккаунт.

```bash
curl -s -X POST http://localhost:8080/api/v1/auth/email/code \
  -H 'Content-Type: application/json' \
  -d '{"email":"dzamikhidirov8@gmail.com","purpose":"login"}'
```

`202 Accepted`:

```json
{
  "data": {
    "sent": true,
    "email": "dzamikhidirov8@gmail.com",
    "purpose": "login",
    "expires_at": "2026-01-01T10:10:00Z",
    "expires_in": 600,
    "resend_after_seconds": 60,
    "dev_code": "482913"
  }
}
```

`dev_code` появляется только в `development` при `EMAIL_PROVIDER=log` и никогда
не возвращается в production. Код хранится как HMAC-SHA256, а для воркера — как
AES-GCM-шифртекст; событие доставки пишется в `email_outbox` в той же
транзакции и публикуется в Kafka relay-процессом.

Ошибки: `422` неверный email/purpose, `429` cooldown или rate limit,
`503 EMAIL_DISABLED`.

### `POST /api/v1/auth/email/login` 🔓

Меняет код на пару токенов. Код одноразовый: повторная попытка → `401`.

```bash
curl -s -X POST http://localhost:8080/api/v1/auth/email/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"dzamikhidirov8@gmail.com","code":"482913"}'
```

`200` — структура `AuthResult`, как у обычного login. После успешного входа
адрес помечается подтверждённым.

### `POST /api/v1/auth/email/verify` 🔑

Запрашивает код подтверждения для текущего пользователя. Если `email` не
передан, используется адрес из профиля.

```bash
curl -s -X POST http://localhost:8080/api/v1/auth/email/verify \
  -H "Authorization: Bearer $ACCESS" \
  -H 'Content-Type: application/json' \
  -d '{"email":"dzamikhidirov8@gmail.com"}'
```

`202` — тот же `EmailCodeResult`.

### `POST /api/v1/auth/email/verify/confirm` 🔑

Проверяет код и выставляет `email_verified=true`. Если адрес уже привязан к
другому аккаунту → `409`.

```bash
curl -s -X POST http://localhost:8080/api/v1/auth/email/verify/confirm \
  -H "Authorization: Bearer $ACCESS" \
  -H 'Content-Type: application/json' \
  -d '{"email":"dzamikhidirov8@gmail.com","code":"482913"}'
```

`200` — обновлённый профиль, `401` — неверный/просроченный код.

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

### `GET /api/v1/auth/sessions` 🔑

Активные сессии (устройства) текущего пользователя, новые сверху. Поле
`current: true` отмечает сессию, из которой сделан запрос.

```bash
curl -s http://localhost:8080/api/v1/auth/sessions \
  -H "Authorization: Bearer $ACCESS"
```

```json
{
  "data": {
    "sessions": [
      {
        "id": "3f6b1c2a-7d4e-4a9b-8c1d-2e3f4a5b6c7d",
        "current": true,
        "ip": "203.0.113.7",
        "last_ip": "203.0.113.7",
        "user_agent": "GetContact/1.2.3 (Android 15; Pixel 9 Pro)",
        "device_id": "8f3c2a7e-1b2d-4c5e-9f00-1234567890ab",
        "device_name": "Pixel 9 Pro",
        "device_model": "Pixel 9 Pro",
        "manufacturer": "Google",
        "os": "Android",
        "os_version": "15",
        "app_version": "1.2.3",
        "app_build": "12345",
        "client": "android",
        "locale": "ru-RU",
        "timezone": "Europe/Moscow",
        "network_type": "wifi",
        "last_seen_at": "2026-01-01T10:00:00Z",
        "created_at": "2026-01-01T10:00:00Z",
        "expires_at": "2026-01-31T10:00:00Z"
      }
    ],
    "total": 1
  }
}
```

### `DELETE /api/v1/auth/sessions/{sessionID}` 🔑

Завершает свою сессию: отзывает refresh и немедленно блокирует связанные
access-токены через Redis. Чужая или несуществующая сессия → `404`.

```bash
curl -s -X DELETE http://localhost:8080/api/v1/auth/sessions/$SESSION_ID \
  -H "Authorization: Bearer $ACCESS"
```

`204 No Content`.

### Метаданные устройства (по согласию клиента)

При register/login/refresh можно передать необязательный контекст устройства —
заголовками или объектом `device`. API сохраняет только эти явные значения плюс
IP и `User-Agent` запроса; он не читает контакты, галерею, файлы и сообщения.

```bash
curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -H 'X-Device-Id: 8f3c2a7e-1b2d-4c5e-9f00-1234567890ab' \
  -H 'X-Device-Name: Pixel 9 Pro' \
  -H 'X-OS: Android' \
  -H 'X-OS-Version: 15' \
  -H 'X-App-Version: 1.2.3' \
  -H 'X-Client: android' \
  -H 'X-Locale: ru-RU' \
  -H 'X-Timezone: Europe/Moscow' \
  -H 'X-Network-Type: wifi' \
  -d '{"phone":"+79991234567","password":"Passw0rd123"}'
```

Эквивалент в теле:

```json
{
  "phone": "+79991234567",
  "password": "Passw0rd123",
  "device": {
    "id": "8f3c2a7e-1b2d-4c5e-9f00-1234567890ab",
    "name": "Pixel 9 Pro",
    "model": "Pixel 9 Pro",
    "manufacturer": "Google",
    "os": "Android",
    "os_version": "15",
    "app_version": "1.2.3",
    "app_build": "12345",
    "client": "android",
    "locale": "ru-RU",
    "timezone": "Europe/Moscow",
    "network_type": "wifi"
  }
}
```

IP берётся из `X-Forwarded-For` (первый валидный адрес) или `X-Real-IP`, затем
из `RemoteAddr`. Значения обрезаются до безопасной длины.

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

### `GET /api/v1/users/me/stats` 🔑

Личный дашборд: `contacts`, `favorites`, `tags_added`, `spam_reports`,
`names_submitted`, `active_sessions`, `member_since`. Счётчики читаются
параллельно, поэтому endpoint не деградирует с ростом телефонной книги.

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

**Нормализация телефона.** Принимаются `+7…`, `8…`, `00…`, скобки, пробелы,
дефисы, точки, слэши и full-width цифры. Явный `+` или `00` отключает
российские эвристики, поэтому `+47…`, `+81…` и другие международные номера
не переписываются. Результат всегда `+<8..15 цифр>`.

### `GET /api/v1/numbers/trending` 🔓

Номера с самым большим счётчиком просмотров. Query: `limit` (1..50, по
умолчанию 10).

### `POST /api/v1/numbers/lookup` 🔑

Пакетное определение до 100 номеров за запрос — для синхронизации журнала
звонков. Невалидный номер не ломает пакет: в его элементе будет `error`.

```bash
curl -s -X POST http://localhost:8080/api/v1/numbers/lookup \
  -H "Authorization: Bearer $ACCESS" -H 'Content-Type: application/json' \
  -d '{"phones":["+79991234567","8 999 765-43-21","bad"]}'
```

```json
{
  "data": {
    "results": [
      { "phone": "+79991234567", "number": { "...": "..." }, "tags": [] },
      { "phone": "+79997654321", "number": { "...": "..." }, "tags": [] },
      { "phone": "bad", "error": "must be a valid phone number" }
    ],
    "requested": 3,
    "returned": 2
  }
}
```

### `GET /api/v1/numbers/{phone}/names` 🔓

Агрегированные имена из сообщества: `[{ "name": "Иван", "count": 3 }]`.

### `POST /api/v1/numbers/{phone}/names` 🔑

Один голос на пользователя и номер; повторная отправка **заменяет** голос.
После изменения пересчитывается отображаемое имя номера.

```bash
curl -s -X POST http://localhost:8080/api/v1/numbers/+79991234567/names \
  -H "Authorization: Bearer $ACCESS" -H 'Content-Type: application/json' \
  -d '{"name":"Иван Петров"}'
```

`201` — голос; `name` — 2..64 символа, управляющие символы и `<>` запрещены.

### `DELETE /api/v1/numbers/{phone}/names/{nameID}` 🔑

Доступно автору голоса или администратору.

### `GET /api/v1/numbers/{phone}/spam` 🔓

Сводка репортов без раскрытия авторов:

```json
{
  "data": {
    "phone": "+79995555555",
    "total": 12,
    "by_category": [ { "category": "telemarketing", "count": 9 } ],
    "last_reported_at": "2026-01-01T10:00:00Z"
  }
}
```

### `GET /api/v1/spam/categories` 🔓

Каталог допустимых категорий: `code`, `label`, `description`, `severity`.

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
| `limit` | 10 | 100 |

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
| `limit` | нет | 1..50, по умолчанию 20 |

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

### `DELETE /api/v1/admin/reports/{reportID}`

Удаляет репорт, пересчитывает `spam_score`, инвалидирует кэш caller-ID и
публикует `number.spam_deleted`. `204`.

### `GET /api/v1/admin/numbers/{phone}`

**Полное досье номера** для администратора: профиль, теги, все голоса за имя,
агрегаты имён, последние репорты, сводка по категориям и последние
audit-события. Каждое успешное чтение пишет `admin.number_viewed` с ID
администратора — это не анонимный «бэкдор», а журналируемый доступ.

```json
{
  "data": {
    "number": { "phone": "+79995555555", "spam_score": 60, "...": "..." },
    "tags": [ { "tag": "telemarketing", "weight": 4, "...": "..." } ],
    "names": [ { "id": "...", "name": "Иван", "submitted_by": "...", "...": "..." } ],
    "name_counts": [ { "name": "Иван", "count": 2 } ],
    "reports": [ { "id": "...", "category": "telemarketing", "...": "..." } ],
    "spam": { "total": 4, "by_category": [ { "category": "telemarketing", "count": 4 } ] },
    "audit": [ { "event_type": "admin.number_viewed", "...": "..." } ]
  }
}
```

### `PATCH /api/v1/admin/numbers/{phone}`

Частичное редактирование: `name`, `carrier`, `country`. Отсутствующее поле не
меняется, пустая строка очищает. `400` — пустой body; `404` — номера нет.

### `GET /api/v1/admin/audit`

Лента всех событий, дошедших до `audit_events` через Kafka worker, новые сверху.
Query: `event_type`, `aggregate_id`, `page`, `per_page` (1..100). Фильтры
комбинируются по AND. Используется для расследований и проверки доступа.

### `GET /api/v1/admin/users/{id}/summary`

Профиль пользователя, счётчики активности (`contacts`, `tags_added`,
`spam_reports`, `names_submitted`, `active_sessions`), список активных сессий
(`sessions` с IP, устройством, ОС и версией приложения) и последние
audit-события по его `user_id`.

### `GET /api/v1/admin/users/{id}/sessions` 👑

Только чтение: активные сессии любого пользователя в том же формате, что и
`GET /api/v1/auth/sessions` (без флага `current`).

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
