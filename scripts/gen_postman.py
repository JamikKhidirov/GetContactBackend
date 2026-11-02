#!/usr/bin/env python3
"""Generate the Postman collection and environment for the GetContact API.

Usage: python scripts/gen_postman.py
"""
import json
import pathlib
import uuid

ROOT = pathlib.Path(__file__).resolve().parent.parent
OUT = ROOT / "api"

variables = [
    ("baseUrl", "http://localhost:8080"),
    ("accessToken", ""),
    ("refreshToken", ""),
    ("adminAccessToken", ""),
    ("userId", ""),
    ("contactId", ""),
    ("tagId", ""),
    ("nameId", ""),
    ("reportId", ""),
    ("userPassword", "Passw0rd123"),
    ("phone", "+79991234567"),
    ("contactPhone", "+79997654321"),
    ("spamPhone", "+79995555555"),
    ("adminPhone", "+79990000000"),
    ("adminPassword", "Admin12345"),
    ("tagName", "telemarketing"),
    ("searchQuery", "Ivan"),
]


def save_tokens(var="accessToken", refresh="refreshToken", admin=False):
    target = "adminAccessToken" if admin else var
    lines = [
        "const json = pm.response.json();",
        "pm.test('status is 2xx', () => pm.response.to.be.success);",
        "if (json.data && json.data.tokens) {",
        f"  pm.collectionVariables.set('{target}', json.data.tokens.access_token);",
    ]
    if not admin:
        lines += [
            f"  pm.collectionVariables.set('{refresh}', json.data.tokens.refresh_token);",
            "  pm.collectionVariables.set('userId', json.data.user.id);",
        ]
    lines.append("}")
    return ["\n".join(lines)]


def auth_block(kind):
    if kind == "none":
        return {"type": "noauth"}
    token = "{{adminAccessToken}}" if kind == "admin" else "{{accessToken}}"
    return {"type": "bearer", "bearer": [{"key": "token", "value": token, "type": "string"}]}


def make_request(name, method, path, *, body=None, query=None, auth="user", tests=None, description=None):
    raw = "{{baseUrl}}" + path
    if query:
        raw += "?" + "&".join(f"{k}={v}" for k, v in query)
    url = {
        "raw": raw,
        "host": ["{{baseUrl}}"],
        "path": [p for p in path.strip("/").split("/") if p],
    }
    if query:
        url["query"] = [{"key": k, "value": str(v)} for k, v in query]
    item = {
        "name": name,
        "request": {
            "method": method,
            "header": [{"key": "Accept", "value": "application/json"}],
            "url": url,
            "auth": auth_block(auth),
            "description": description or "",
        },
    }
    if body is not None:
        item["request"]["header"].append({"key": "Content-Type", "value": "application/json"})
        item["request"]["body"] = {
            "mode": "raw",
            "raw": json.dumps(body, ensure_ascii=False, indent=2),
            "options": {"raw": {"language": "json"}},
        }
    if tests:
        item["event"] = [{"listen": "test", "script": {"type": "text/javascript", "exec": tests}}]
    return item


def folder(name, description, items):
    return {"name": name, "description": description, "item": items}


collection = {
    "info": {
        "_postman_id": str(uuid.uuid4()),
        "name": "GetContact API",
        "description": (
            "Полная коллекция GetContact API.\n\n"
            "1. Запустите `docker compose -f deploy/docker/docker-compose.yml up -d --build`.\n"
            "2. Выберите окружение `GetContact Local`.\n"
            "3. Выполните **Auth → Register** (или Login) — токены сохранятся автоматически.\n"
            "4. Остальные запросы используют `{{accessToken}}`.\n\n"
            "Admin-запросы используют `{{adminAccessToken}}` (Admin → Login).\n"
            "Папка **99 Destructive** удаляет данные — запускайте осознанно."
        ),
        "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json",
    },
    "auth": auth_block("user"),
    "event": [
        {
            "listen": "test",
            "script": {
                "type": "text/javascript",
                "exec": [
                    "pm.test('response time < 2000ms', () => pm.expect(pm.response.responseTime).to.be.below(2000));",
                    "if (pm.response.code >= 400) { console.warn('Error:', pm.response.text()); }",
                ],
            },
        }
    ],
    "variable": [{"key": k, "value": v, "type": "string"} for k, v in variables],
    "item": [
        folder(
            "00 Health",
            "Операционные endpoints. Не требуют токена.",
            [
                make_request("Service index", "GET", "/", auth="none"),
                make_request("Liveness", "GET", "/healthz", auth="none"),
                make_request("Readiness", "GET", "/readyz", auth="none"),
                make_request("Version", "GET", "/version", auth="none"),
                make_request("Prometheus metrics", "GET", "/metrics", auth="none"),
                make_request("OpenAPI YAML", "GET", "/openapi.yaml", auth="none"),
                make_request("Swagger UI", "GET", "/docs", auth="none"),
            ],
        ),
        folder(
            "01 Auth",
            "Регистрация/вход. Register и Login автоматически сохраняют токены в переменные коллекции.",
            [
                make_request(
                    "Register",
                    "POST",
                    "/api/v1/auth/register",
                    auth="none",
                    body={
                        "phone": "{{phone}}",
                        "password": "{{userPassword}}",
                        "display_name": "Ivan Petrov",
                        "email": "ivan@example.com",
                    },
                    tests=save_tokens(),
                    description="201 Created. Если пользователь уже существует — 409, используйте Login.",
                ),
                make_request(
                    "Login",
                    "POST",
                    "/api/v1/auth/login",
                    auth="none",
                    body={"phone": "{{phone}}", "password": "{{userPassword}}"},
                    tests=save_tokens(),
                ),
                make_request(
                    "Refresh tokens",
                    "POST",
                    "/api/v1/auth/refresh",
                    auth="none",
                    body={"refresh_token": "{{refreshToken}}"},
                    tests=[
                        "const json = pm.response.json();",
                        "pm.test('status is 200', () => pm.response.to.have.status(200));",
                        "if (json.data && json.data.tokens) {",
                        "  pm.collectionVariables.set('accessToken', json.data.tokens.access_token);",
                        "  pm.collectionVariables.set('refreshToken', json.data.tokens.refresh_token);",
                        "}",
                    ],
                    description="Refresh одноразовый: повторный вызов со старым токеном вернёт 401.",
                ),
                make_request("Me", "GET", "/api/v1/auth/me"),
                make_request(
                    "Logout",
                    "POST",
                    "/api/v1/auth/logout",
                    body={"refresh_token": "{{refreshToken}}"},
                    tests=[
                        "pm.test('status is 204', () => pm.response.to.have.status(204));",
                        "pm.collectionVariables.set('accessToken', '');",
                        "pm.collectionVariables.set('refreshToken', '');",
                    ],
                    description="Отзывает refresh и blacklist-ит access JTI.",
                ),
            ],
        ),
        folder(
            "02 Users",
            "Профиль текущего пользователя.",
            [
                make_request("Get user by id", "GET", "/api/v1/users/{{userId}}"),
                make_request(
                    "My stats",
                    "GET",
                    "/api/v1/users/me/stats",
                    description="Личные счётчики: контакты, избранное, теги, репорты, имена, сессии.",
                ),
                make_request(
                    "Update my profile",
                    "PATCH",
                    "/api/v1/users/me",
                    body={"display_name": "Ivan Updated", "email": "ivan.updated@example.com"},
                ),
                make_request(
                    "Change password",
                    "PUT",
                    "/api/v1/users/me/password",
                    body={"current_password": "{{userPassword}}", "new_password": "{{userPassword}}"},
                    description="Отзывает все refresh-сессии. В демо новый пароль совпадает со старым.",
                ),
            ],
        ),
        folder(
            "03 Contacts",
            "Личная телефонная книга. Create сохраняет {{contactId}}.",
            [
                make_request(
                    "List contacts",
                    "GET",
                    "/api/v1/contacts",
                    query=[("page", 1), ("per_page", 20), ("q", ""), ("favorite", "true")],
                ),
                make_request(
                    "Create contact",
                    "POST",
                    "/api/v1/contacts",
                    body={
                        "phone": "{{contactPhone}}",
                        "name": "Alice Johnson",
                        "email": "alice@example.com",
                        "note": "met at conference",
                        "is_favorite": True,
                    },
                    tests=[
                        "pm.test('status is 201', () => pm.response.to.have.status(201));",
                        "const id = pm.response.json().data.id;",
                        "pm.collectionVariables.set('contactId', id);",
                    ],
                ),
                make_request(
                    "Import contacts",
                    "POST",
                    "/api/v1/contacts/import",
                    body={
                        "contacts": [
                            {"phone": "+79990000001", "name": "Bob Stone"},
                            {"phone": "+79990000002", "name": "Carol White", "is_favorite": False},
                        ]
                    },
                    description="До 500 контактов, upsert по (owner, phone).",
                ),
                make_request("Get contact", "GET", "/api/v1/contacts/{{contactId}}"),
                make_request(
                    "Update contact",
                    "PATCH",
                    "/api/v1/contacts/{{contactId}}",
                    body={"note": "updated note", "is_favorite": False},
                ),
                make_request(
                    "Delete contact",
                    "DELETE",
                    "/api/v1/contacts/{{contactId}}",
                    tests=["pm.test('status is 204', () => pm.response.to.have.status(204));"],
                ),
            ],
        ),
        folder(
            "04 Numbers",
            "Caller-ID, community-теги и спам-репорты. Add tag сохраняет {{tagId}}.",
            [
                make_request(
                    "Lookup number (public)",
                    "GET",
                    "/api/v1/numbers/{{phone}}",
                    auth="none",
                    description="Публичный, опционально можно передать Bearer.",
                ),
                make_request("List number tags (public)", "GET", "/api/v1/numbers/{{phone}}/tags", auth="none"),
                make_request(
                    "Add tag",
                    "POST",
                    "/api/v1/numbers/{{phone}}/tags",
                    body={"tag": "{{tagName}}"},
                    tests=[
                        "pm.test('status is 201', () => pm.response.to.have.status(201));",
                        "pm.collectionVariables.set('tagId', pm.response.json().data.id);",
                    ],
                ),
                make_request(
                    "Delete tag",
                    "DELETE",
                    "/api/v1/numbers/{{phone}}/tags/{{tagId}}",
                    tests=["pm.test('status is 204', () => pm.response.to.have.status(204));"],
                ),
                make_request(
                    "Report spam",
                    "POST",
                    "/api/v1/numbers/{{spamPhone}}/spam",
                    body={"category": "telemarketing", "comment": "Calls every day"},
                    description="201 — репорт принят. Повторный репорт вернёт 409.",
                ),
                make_request("Top spam numbers (public)", "GET", "/api/v1/numbers/spam/top", query=[("limit", 10)], auth="none"),
                make_request(
                    "Trending numbers (public)",
                    "GET",
                    "/api/v1/numbers/trending",
                    query=[("limit", 10)],
                    auth="none",
                    description="Номера с наибольшим числом lookup.",
                ),
                make_request(
                    "Batch lookup",
                    "POST",
                    "/api/v1/numbers/lookup",
                    body={"phones": ["{{phone}}", "{{spamPhone}}", "8 999 765-43-21"]},
                    description="До 100 номеров за запрос; невалидные возвращаются с полем error.",
                ),
                make_request("Number names (public)", "GET", "/api/v1/numbers/{{phone}}/names", auth="none"),
                make_request(
                    "Add number name",
                    "POST",
                    "/api/v1/numbers/{{phone}}/names",
                    body={"name": "Ivan Petrov"},
                    tests=[
                        "pm.test('status is 201', () => pm.response.to.have.status(201));",
                        "pm.collectionVariables.set('nameId', pm.response.json().data.id);",
                    ],
                    description="Один голос на пользователя и номер; повтор заменяет вариант.",
                ),
                make_request(
                    "Delete number name",
                    "DELETE",
                    "/api/v1/numbers/{{phone}}/names/{{nameId}}",
                    tests=["pm.test('status is 204', () => pm.response.to.have.status(204));"],
                ),
                make_request("Number spam summary (public)", "GET", "/api/v1/numbers/{{spamPhone}}/spam", auth="none"),
                make_request("Spam categories (public)", "GET", "/api/v1/spam/categories", auth="none"),
                make_request("Popular tags (public)", "GET", "/api/v1/tags", query=[("limit", 20)], auth="none"),
            ],
        ),
        folder(
            "05 Search",
            "Единый поиск по общему справочнику и личным контактам.",
            [
                make_request("Search", "GET", "/api/v1/search", query=[("q", "{{searchQuery}}"), ("limit", 20)]),
            ],
        ),
        folder(
            "06 Admin",
            "Только роль admin. Сначала выполните Admin → Login (сохраняет adminAccessToken).",
            [
                make_request(
                    "Admin login",
                    "POST",
                    "/api/v1/auth/login",
                    auth="none",
                    body={"phone": "{{adminPhone}}", "password": "{{adminPassword}}"},
                    tests=save_tokens(admin=True),
                ),
                make_request("Dashboard stats", "GET", "/api/v1/admin/stats", auth="admin"),
                make_request("List users", "GET", "/api/v1/admin/users", query=[("page", 1), ("per_page", 20), ("q", "")], auth="admin"),
                make_request("Get user", "GET", "/api/v1/admin/users/{{userId}}", auth="admin"),
                make_request(
                    "Block user",
                    "PATCH",
                    "/api/v1/admin/users/{{userId}}/block",
                    auth="admin",
                    body={"blocked": True},
                ),
                make_request(
                    "Unblock user",
                    "PATCH",
                    "/api/v1/admin/users/{{userId}}/block",
                    auth="admin",
                    body={"blocked": False},
                ),
                make_request(
                    "User summary",
                    "GET",
                    "/api/v1/admin/users/{{userId}}/summary",
                    auth="admin",
                    description="Профиль, счётчики активности и последние audit-события.",
                ),
                make_request(
                    "Spam report queue",
                    "GET",
                    "/api/v1/admin/reports",
                    query=[("page", 1), ("per_page", 20)],
                    auth="admin",
                    tests=[
                        "const json = pm.response.json();",
                        "if (json.data && json.data.length) { pm.collectionVariables.set('reportId', json.data[0].id); }",
                    ],
                ),
                make_request(
                    "Delete spam report",
                    "DELETE",
                    "/api/v1/admin/reports/{{reportId}}",
                    auth="admin",
                    tests=["pm.test('status is 204', () => pm.response.to.have.status(204));"],
                    description="Пересчитывает spam score и инвалидирует кэш номера.",
                ),
                make_request(
                    "Number dossier",
                    "GET",
                    "/api/v1/admin/numbers/{{spamPhone}}",
                    auth="admin",
                    description="Полное досье; каждое чтение пишет admin.number_viewed в аудит.",
                ),
                make_request(
                    "Edit number",
                    "PATCH",
                    "/api/v1/admin/numbers/{{spamPhone}}",
                    auth="admin",
                    body={"name": "Spam Caller", "carrier": "MTS", "country": "RU"},
                ),
                make_request(
                    "Audit feed",
                    "GET",
                    "/api/v1/admin/audit",
                    query=[("page", 1), ("per_page", 20), ("event_type", "admin.number_viewed")],
                    auth="admin",
                ),
            ],
        ),
        folder(
            "99 Destructive",
            "Удаление данных. Запускать только осознанно.",
            [
                make_request(
                    "Delete my account",
                    "DELETE",
                    "/api/v1/users/me",
                    tests=["pm.test('status is 204', () => pm.response.to.have.status(204));"],
                    description="Каскадно удаляет контакты, токены и репорты.",
                ),
                make_request(
                    "Admin delete user",
                    "DELETE",
                    "/api/v1/admin/users/{{userId}}",
                    auth="admin",
                    description="Нельзя удалить самого себя через admin API.",
                ),
            ],
        ),
    ],
}

environment = {
    "id": str(uuid.uuid4()),
    "name": "GetContact Local",
    "values": [
        {"key": k, "value": v, "type": "default", "enabled": True}
        for k, v in variables
    ],
    "_postman_variable_scope": "environment",
}

(OUT / "postman_collection.json").write_text(json.dumps(collection, ensure_ascii=False, indent=2), encoding="utf-8")
(OUT / "postman_environment.json").write_text(json.dumps(environment, ensure_ascii=False, indent=2), encoding="utf-8")
print("wrote api/postman_collection.json and api/postman_environment.json")
