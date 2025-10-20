# Currency TA Go — Сервис асинхронных котировок

Асинхронный HTTP-сервис на Go для обновления и получения валютных курсов.  
Обновление выполняется **в фоне**, обработчики HTTP возвращают ответ сразу.

---

## 📌 Возможности

- `POST /quotes/update` — создать задачу обновления курса по валютной паре (например, `EUR/MXN`) и сразу получить `update_id`.
- `GET /quotes/update/{id}` — получить статус обновления (`pending | done | error`) и данные, если готово.
- `GET /quotes/{pair}` — получить **последнее сохранённое** значение курса и время обновления.
- Идемпотентность запросов обновления через заголовок **`Idempotency-Key`**.
- Источник курсов — **Frankfurter** (`/latest`) (нет ретрая и кеша) https://frankfurter.dev/
- Кэш на уровне нашего API get/quotes/{id}.
- БД: PostgreSQL (миграции выполняются при старте).
- Базовый **rate limit** (например, `60 req/min`).
- **Swagger UI** на `http://localhost:8081/swagger/index.html` (не автогенерация)
- Docker/Docker Compose.

---

## 🧱 Структура проекта

```
.
├── cmd/
│   └── server/
│       └── main.go             # запуск: чтение env, инициализация, HTTP-сервер
├── internal/
│   ├── httpx/                  # HTTP-маршруты, middleware, JSON-ответы
│   ├── service/                # бизнес-логика, валидация, очередь, воркер
│   ├── repo/                   # доступ к PostgreSQL, миграции
│   ├── external/               # клиент Frankfurter (/latest) + ретраи + логирование
│   ├── cache/                  # простой TTL-кэш (используется ТОЛЬКО провайдером)
│   ├── ratelimit/              # базовый лимитер по IP
│   └── migrations/             # SQL-миграции (создание таблиц и индексов)
├── docs/                       # swagger-артефакты (генерятся в Docker build)
├── Dockerfile
├── Dockerfile.test
├── docker-compose.yml
└── README.md
```

### База данных (основные таблицы)

- `updates` — **журнал задач** (append-only): `id`, `pair`, `status`, `price`, `updated_at`, `error`, `idempotency_key`, `created_at`.
  - `UNIQUE (pair, idempotency_key) WHERE idempotency_key IS NOT NULL` — идемпотентность.
- `quotes` — **снимок последнего значения** по паре: `pair (PK)`, `price`, `updated_at`.

---

## ⚙️ Компоненты и как они связаны

- **Handlers (httpx)** принимают HTTP-запросы, валидируют вход, вызывают **service** и возвращают JSON.
- **Service**:
  - создаёт запись `pending` в `updates`, кладёт задачу в канал и сразу возвращает `update_id`;
  - воркер в фоне забирает задачи, вызывает **external.Exchanger**, сохраняет результат в `updates` и обновляет `quotes` (UPSERT).
  - поддерживает идемпотентность по `(pair, Idempotency-Key)`.
- **External.Exchanger**:
  - обращается к Frankfurter `GET /latest?base=AAA&symbols=BBB`;
  - опционально логирует URL и тело ответа (`FX_DEBUG=1`);
  - кэширует **ответ провайдера** по паре на 24 часа (чтобы не дёргать Frankfurter чаще их обновления).
- **Repo** — работа с PostgreSQL (pgxpool), миграции при старте.
- **Rate limit** — базовая защита от частых вызовов.

---

## 🔑 Переменные окружения

| Переменная        | Назначение                                                    | Пример                                          |
|-------------------|----------------------------------------------------------------|--------------------------------------------------|
| `ADDR`            | Адрес HTTP-сервера                                            | `:8080`                                         |
| `DATABASE_URL`    | Подключение к Postgres                                         | `postgres://postgres:postgres@db:5432/quotes?sslmode=disable` |
| `FX_BASE_URL`     | База Frankfurter                                               | `https://api.frankfurter.dev/v1`                |
| `FX_PAIRS`        | Разрешённые пары (список через запятую)                        | `USD/EUR,EUR/USD,EUR/MXN,USD/MXN`               |
| `RATE_LIMIT`      | Лимит запросов на окно                                         | `60`                                            |
| `RATE_WINDOW`     | Длина окна лимитера                                            | `1m`                                            |
| `FX_RETRIES`      | Количество попыток запроса к провайдеру                        | `3`                                             |
| `FX_RETRY_BACKOFF`| Базовая задержка ретраев                                       | `300ms`                                         |
| `FX_DEBUG`        | Логировать запросы/ответы провайдера (`1`/`true` для включения)| `0`                                             |


---

## 🐳 Запуск в Docker / Docker Compose

```bash
# сборка и запуск
docker compose build --no-cache
docker compose up

# API будет на http://localhost:8080
# Swagger UI: http://localhost:8081/swagger/index.html
```

> При старте сервис запускает миграции.

---

## 🔗 HTTP-эндпоинты

### 1) Создать обновление курса 

`POST /quotes/update`  
Body: `{"pair":"EUR/MXN"}`  
Опционально: `Idempotency-Key: <строка>` — для идемпотентности.

**Пример:**
```bash
curl -s -X POST http://localhost:8080/quotes/update   -H 'Content-Type: application/json'   -H 'Idempotency-Key: demo-1'   -d '{"pair":"EUR/MXN"}'
```

**200 OK**
```json
{ "update_id": "f9e2a3f4-..." }
```

Ошибки:
- `400 {"error":"invalid pair format, expected AAA/BBB"}` — неверный формат пары;
- `400 {"error":"pair not supported"}` — пары нет в списке `FX_PAIRS`;
- `429 {"error":"rate limit exceeded"}` — лимитер;
- `500 {"error":"..."}` — внутренняя ошибка.

---

### 2) Получить статус обновления

`GET /quotes/update/{update_id}`

**Пример:**
```bash
curl -s http://localhost:8080/quotes/update/f9e2a3f4-...
```

**200 OK (pending)**
```json
{ "status":"pending", "pair":"EUR/MXN" }
```

**200 OK (done)**
```json
{
  "status":"done",
  "pair":"EUR/MXN",
  "price":21.505,
  "updated_at":"2025-10-19T14:38:16.323906Z"
}
```

**200 OK (error)**
```json
{
  "status":"error",
  "pair":"EUR/MXN",
  "updated_at":"2025-10-19T14:38:16.323906Z",
  "error":"external rate not available; please retry later"
}
```

Ошибки:  
`404 {"error":"not found"}` — нет такой задачи; `500` — внутренняя.

---

### 3) Получить последнее значение курса

`GET /quotes/{pair}` — читает из таблицы `quotes` (**без кэша API**).

**Пример:**
```bash
curl -s http://localhost:8080/quotes/EUR/MXN
```

**200 OK**
```json
{
  "pair":"EUR/MXN",
  "price":21.505,
  "updated_at":"2025-10-19T14:38:16.323906Z"
}
```

**404 Not Found**
```json
{ "error":"not found" }
```

---

## 🧠 Как это работает =

1. Клиент вызывает `POST /quotes/update {pair}` (с опциональным `Idempotency-Key`).  
   Хендлер создаёт запись `pending` в `updates`, кладёт задачу в очередь (канал) и **сразу** возвращает `update_id`.

2. Фоновый воркер забирает задачу, вызывает Frankfurter `/latest?base=AAA&symbols=BBB` 
   Если успех — помечает задачу `done` и делает UPSERT в `quotes`. Если нет — `error`.

3. Клиент может:
   - опрашивать `GET /quotes/update/{id}` до `done`;
   - в любой момент читать текущее значение `GET /quotes/{pair}` (последний снимок в БД).

4. Идемпотентность: повторный `POST` с тем же `(pair, Idempotency-Key)` вернёт **тот же** `update_id` (уникальный индекс в БД).


---


## 🛠️ Swagger 

- Открыть: http://localhost:8081/swagger/index.html
## Тесты
- docker build -f Dockerfile.test --target coverage -t currency-coverage .
- docker run --rm -p 8090:80 currency-coverage  
Покрыты кеш и рейтлимитер

## 🧰 Полезные команды

```bash
# создать задачу, получить ID
curl -s -X POST http://localhost:8080/quotes/update   -H 'Content-Type: application/json'   -H 'Idempotency-Key: demo-1'   -d '{"pair":"EUR/MXN"}'

# статус по ID
curl -s http://localhost:8080/quotes/update/<UUID>

# последнее значение
curl -s http://localhost:8080/quotes/EUR/MXN
```

---

## 🔎 Траблшутинг

- **`404` на /quotes/{pair}** — данных ещё нет: сначала создайте обновление и дождитесь `done`.
- **`error":"no rate ..."`** — провайдер не вернул курс (пара/параметры/временный сбой); повторите позже.
- **`429`** — превысили лимит; снизьте частоту.
