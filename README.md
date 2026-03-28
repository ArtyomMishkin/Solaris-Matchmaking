# Solaris Matchmaking Backend

Проект написан на Go, хранит данные в PostgreSQL и предоставляет HTTP API для:
- регистрации/авторизации игроков;
- работы с лобби;
- генерации условий миссий для лобби без MMR;
- админских операций (звания, удаление лобби/истории, удаление игроков).

---

## 1) Технологии

- Go (стандартный `net/http`, без внешнего фреймворка)
- PostgreSQL
- JWT (`github.com/golang-jwt/jwt/v5`)
- Bcrypt для хеширования паролей

---

## 2) Быстрый старт

### 2.1 Требования

- Go (рекомендуется 1.22+)
- PostgreSQL (локально/в Docker/на сервере)

### 2.2 Переменные окружения

- `DATABASE_URL` - строка подключения к PostgreSQL
- `JWT_SECRET` - секрет для подписи JWT (обязательно задать в проде)

Пример для PowerShell:

```powershell
$env:DATABASE_URL="postgres://postgres:postgres@localhost:5432/solaris_matchmaking?sslmode=disable"
$env:JWT_SECRET="your-very-strong-secret"
```

### 2.3 Запуск

```bash
go mod tidy
go run ./cmd/api
```

Сервис поднимается на `:8080`.

Проверка:

```bash
curl http://localhost:8080/health
```

### 2.4 Создание тестовых данных в PostgreSQL (seed)

После того как PostgreSQL доступен и `DATABASE_URL` задан, можно заполнить базу демонстрационными данными:

```bash
go run ./cmd/seed
```

Что создается:
- несколько игроков;
- учетные данные с паролем `StrongPass123!`;
- один из игроков получает роль `admin`;
- опыт по фракциям (`player_faction_experience`);
- пример non-MMR лобби с 2 участниками и кастомными условиями.

---

## 3) Архитектура проекта

- `cmd/api` - запуск HTTP API
- `cmd/admin` - локальные админские CLI-команды
- `internal/db` - подключение к БД, миграции, админские DB-утилиты
- `internal/httpapi` - HTTP handlers, auth, бизнес-логика API

---

## 4) Схема БД

Миграции выполняются автоматически при старте (`db.OpenAndMigrate`).

### 4.1 `players`

Основная таблица профиля игрока.

- `id BIGSERIAL PRIMARY KEY`
- `full_name TEXT NOT NULL` - ФИО
- `nickname TEXT NOT NULL UNIQUE` - Ник
- `city TEXT NOT NULL` - Город
- `contacts TEXT NOT NULL` - Контакты
- `preferred_location TEXT NOT NULL` - Локация (приоритет)
- `rank_title TEXT` - Звание
- `rank_attested_at TEXT` - Дата аттестации
- `factions TEXT NOT NULL DEFAULT '[]'` - JSON-массив фракций
- `tournaments TEXT NOT NULL DEFAULT '[]'` - JSON-массив турниров
- `hobby_evenings TEXT NOT NULL DEFAULT '[]'` - JSON-массив хобби-вечеров
- `total_experience INTEGER NOT NULL DEFAULT 0` - Опыт
- `rating INTEGER NOT NULL DEFAULT 1500` - рейтинг для ranked (Glicko)
- `rating_rd DOUBLE PRECISION NOT NULL DEFAULT 350` - Rating Deviation (неопределенность рейтинга)
- `other_events TEXT NOT NULL DEFAULT '[]'` - JSON-массив прочих ивентов
- `collection_link TEXT` - Ссылка на коллекцию
- `created_at TEXT NOT NULL`
- `updated_at TEXT NOT NULL`

### 4.2 `player_credentials`

Закрытая таблица учетных данных.

- `id BIGSERIAL PRIMARY KEY`
- `player_id BIGINT NOT NULL UNIQUE REFERENCES players(id) ON DELETE CASCADE`
- `password_hash TEXT NOT NULL` - bcrypt-хеш пароля
- `role TEXT NOT NULL DEFAULT 'player'` - `player` или `admin`
- `created_at TEXT NOT NULL`
- `updated_at TEXT NOT NULL`

### 4.3 `lobbies`

Текущие лобби.

- `id BIGSERIAL PRIMARY KEY`
- `host_player_id BIGINT NOT NULL REFERENCES players(id)`
- `faction TEXT NOT NULL`
- `match_size INTEGER NOT NULL`
- `is_ranked BOOLEAN NOT NULL DEFAULT FALSE`
- `mission_condition_id BIGINT NULL` - ссылка на `mission_conditions.id`
- `custom_mission_name TEXT NULL` - ручная миссия (только non-MMR)
- `custom_weather_name TEXT NULL` - ручная погода (только non-MMR)
- `custom_atmosphere_name TEXT NULL` - ручная атмосфера (только non-MMR)
- `status TEXT NOT NULL DEFAULT 'open'`
- `started_at TEXT NULL` - время старта матча
- `finished_at TEXT NULL` - время окончания матча
- `created_at TEXT NOT NULL`
- `updated_at TEXT NOT NULL`

### 4.4 `lobbies_history`

История лобби (архив/завершенные состояния/данные для отчета).

- `id BIGSERIAL PRIMARY KEY`
- `original_lobby_id BIGINT NOT NULL`
- `host_player_id BIGINT NOT NULL`
- `faction TEXT NOT NULL`
- `match_size INTEGER NOT NULL`
- `status TEXT NOT NULL`
- `created_at TEXT NOT NULL`
- `updated_at TEXT NOT NULL`
- `finished_at TEXT NULL`

### 4.5 `mission_conditions`

Справочник условий миссий.

- `id BIGSERIAL PRIMARY KEY`
- `mode_name TEXT NOT NULL` - режим
- `weather_name TEXT NOT NULL` - погода
- `description TEXT NOT NULL DEFAULT ''`
- `is_active BOOLEAN NOT NULL DEFAULT TRUE`

В миграции добавлены стартовые seed-записи.

### 4.6 `player_faction_experience`

Отдельный опыт игрока по каждой фракции.

- `id BIGSERIAL PRIMARY KEY`
- `player_id BIGINT NOT NULL REFERENCES players(id) ON DELETE CASCADE`
- `faction_name TEXT NOT NULL`
- `experience INTEGER NOT NULL DEFAULT 0`
- `UNIQUE (player_id, faction_name)`

Пример: у игрока может быть `50` опыта в одной фракции и `2` в другой.

### 4.7 `lobby_players`

Состав игроков в лобби и их состояние матча.

- `id BIGSERIAL PRIMARY KEY`
- `lobby_id BIGINT NOT NULL REFERENCES lobbies(id) ON DELETE CASCADE`
- `player_id BIGINT NOT NULL REFERENCES players(id) ON DELETE CASCADE`
- `faction_name TEXT NOT NULL` - выбранная фракция игрока в этом лобби
- `is_ready BOOLEAN NOT NULL DEFAULT FALSE`
- `is_finished BOOLEAN NOT NULL DEFAULT FALSE`
- `joined_at TEXT NOT NULL`
- `UNIQUE (lobby_id, player_id)`

### 4.8 `rating_history`

История изменения рейтинга по ranked-матчам.

- `id BIGSERIAL PRIMARY KEY`
- `lobby_id BIGINT NOT NULL REFERENCES lobbies(id) ON DELETE CASCADE`
- `player_id BIGINT NOT NULL REFERENCES players(id) ON DELETE CASCADE`
- `old_rating INTEGER NOT NULL`
- `new_rating INTEGER NOT NULL`
- `old_rd DOUBLE PRECISION NOT NULL`
- `new_rd DOUBLE PRECISION NOT NULL`
- `score DOUBLE PRECISION NOT NULL` (`1`, `0`, `0.5`)
- `created_at TEXT NOT NULL`

---

## 5) Безопасность и роли

### 5.1 Регистрация и хранение паролей

- Пароль приходит только на `POST /players`
- В БД сохраняется только `bcrypt` хеш
- Открытый пароль не хранится

### 5.2 Авторизация

- Логин через `POST /auth/login`
- В ответе возвращается JWT
- Для защищенных методов нужен заголовок:
  - `Authorization: Bearer <token>`

### 5.3 Роли

- Роль хранится в `player_credentials.role`
- `player` - базовый пользователь
- `admin` - доступ к админским endpoint-ам

---

## 6) Локальные админ-команды (CLI)

Команды запускаются только на сервере/машине с доступом к проекту и БД.

### 6.1 Назначить админа

```powershell
$env:DATABASE_URL="postgres://postgres:postgres@localhost:5432/solaris_matchmaking?sslmode=disable"
go run ./cmd/admin make-admin WolfGuard
```

### 6.2 Снять роль админа

```powershell
$env:DATABASE_URL="postgres://postgres:postgres@localhost:5432/solaris_matchmaking?sslmode=disable"
go run ./cmd/admin remove-admin WolfGuard
```

Защита: нельзя снять роль с последнего админа.

### 6.3 Посмотреть список админов

```powershell
$env:DATABASE_URL="postgres://postgres:postgres@localhost:5432/solaris_matchmaking?sslmode=disable"
go run ./cmd/admin list-admins
```

---

## 7) Правило по условиям миссий (важно)

Условия миссий применяются **только для лобби без MMR**.

- Для `isRanked=false`:
  - можно нажать random -> `POST /lobbies/{id}/random-condition`
- Для `isRanked=true`:
  - условия миссий не назначаются
  - random-condition вернет ошибку

---

## 7.1) Рейтинг ranked-матчей (Glicko-1)

В проекте используется **один шаг обновления Glicko-1** для **двух игроков** в одном матче (как один период с одним соперником в классической статье Glicko). Рейтинг **не** считается автоматически по `match-finished`: для ranked итог фиксируется отдельным вызовом **`POST /lobbies/{id}/ranked-result`**.

### Что хранится у игрока

- **`players.rating`** — числовой рейтинг (в БД целое; при обновлении новое значение **округляется** до ближайшего целого).
- **`players.rating_rd`** — **Rating Deviation** (неопределённость рейтинга; чем выше, тем меньше у системы уверенности в текущем значении `rating`).

Стартовые значения при создании игрока (миграции / дефолты в схеме):

- `rating = 1500`
- `rating_rd = 350`

Перед формулами оба RD проходят через **`clampRD`**: значение принудительно держится в диапазоне **от 30 до 350** (и у себя, и у соперника в расчёте).

### Как трактуется результат матча

Из лобби берутся **ровно два** `player_id` из `lobby_players` в порядке **`ORDER BY joined_at`** — это пары «игрок 1» и «игрок 2» для симметричного расчёта.

В теле запроса:

- **`isDraw: true`** — ничья: обоим выставляется фактический счёт **0.5 / 0.5**.
- **`isDraw: false`** — победа одного: победителю **1**, проигравшему **0**; поле **`winnerPlayerId`** обязано совпадать с одним из двух участников лобби.

Для каждого игрока вызывается одна и та же функция обновления **`glickoUpdate(свой R, свой RD, R соперника, RD соперника, свой score)`**, то есть обновление взаимное и согласованное.

### Математика (как в коде)

Используется константа **`q = ln(10) / 400`** (стандарт для шкалы «как в Elo»).

Для игрока с рейтингом `r` и отклонением `rd` против соперника `oppR`, `oppRD` и своего результата `score` (0, 0.5 или 1):

1. **`g`** — поправка на неопределённость соперника (чем выше `oppRD`, тем слабее «вес» его рейтинга в ожидаемом результате).
2. **`e`** — ожидаемая доля очков против этого соперника (аналог ожидаемого результата, от 0 до 1).
3. **`d2`** — дисперсия (связана с крутизной кривой ожидания в точке `e`).
4. **`pre = 1/rd² + 1/d2`** — совокупная точность после матча.
5. **Новое RD:** `newRD = sqrt(1 / pre)`, затем снова **`clampRD`**.
6. **Новый R:** `newR = r + (q / pre) * g * (score - e)`.

Таким образом, неожиданный результат (победа слабого по рейтингу) сильнее двигает `rating`, а после матча `rating_rd` обычно **сужается**, пока игрок не накопит снова неопределённость (в «полном» Glicko-2 это отдельные периоды; здесь — один шаг на матч).

### Что происходит в БД после успешного запроса

- Обновляются **`players.rating`** и **`players.rating_rd`** у обоих участников.
- В **`rating_history`** добавляются **две** строки (по одной на игрока): старые и новые `rating`/`rating_rd`, **`score`** (0, 0.5 или 1), `lobby_id`, время.
- У лобби выставляются **`rating_applied = TRUE`**, **`status = 'finished'`**, **`finished_at`**.

Повторный **`ranked-result`** для того же лобби отклоняется (**`rating already applied`**), чтобы рейтинг нельзя было начислить дважды.

### API: подтверждение результата

- **`POST /lobbies/{id}/ranked-result`**
- Требуется **JWT**; **`playerId` в токене** должен быть одним из двух участников лобби (иначе `403`).

Тело (победа):

```json
{
  "winnerPlayerId": 1,
  "isDraw": false
}
```

Ничья:

```json
{
  "isDraw": true
}
```

### Ограничения и ошибки (кратко)

- Лобби должно быть **`isRanked = true`**, иначе ответ про Glicko только для ranked.
- В лобби должно быть **ровно 2** игрока.
- Статус лобби **`started`** или **`finished`** (после старта матча); иначе «матч ещё не начался».
- **`rating_applied`** не должен быть уже установлен.

---

## 7.2) Фракции, «место» и время (как в коде)

### Фракции

В системе фигурируют **две разные вещи**:

1. **Строка фракции в лобби** — произвольный текст, который клиент передаёт в JSON.
   - При **`POST /lobbies`** фракция хоста передаётся в объекте **`player{hostPlayerId}.faction`**, то есть ключ совпадает с id хоста (пример: id `1` → **`player1.faction`**).
   - При **`POST /lobbies/{id}/join`** фракция участника передаётся в **`player{playerId}.faction`**, где **`playerId`** из тела запроса и номер в ключе **должны совпадать** (пример: `"playerId": 2` и **`player2.faction`**).
   - Сервер **не** валидирует список допустимых названий: это свободная строка после `TrimSpace`.

2. **Справочник фракций на профиле игрока** — колонка **`players.factions`** в PostgreSQL.
   - В БД это **одна текстовая колонка**, в которой лежит **JSON-массив строк**, например `["Clan Wolf","Mercenaries"]`.
   - При регистрации в коде подставляется литерал **`[]`** (`createPlayer` в `internal/httpapi/players.go`).
   - При **`POST .../match-finished`**, когда оба игрока отметили завершение casual-матча, для каждого игрока его **`faction_name` из `lobby_players`** добавляется в этот массив, если такой строки ещё нет (сравнение **без учёта регистра**, `strings.EqualFold`).
   - В ответах **`GET /players/{id}`** поле **`factions`** уже отдаётся как **нативный JSON-массив** (после `json.Unmarshal` на сервере).

Отдельно таблица **`player_faction_experience`** хранит числовой опыт **по паре** `(player_id, faction_name)` — тоже со свободной строкой фракции, как при `join`.

### «Место»

В коде **нет выбора места из справочника** и нет отдельного API под «локацию матча».

- **`city`** — город из анкеты при регистрации; обязательная строка в **`POST /players`**; хранится в **`players.city`**.
- **`preferredLocation`** — «приоритетная локация» / место игры **текстом**, тоже задаётся только при регистрации; хранится в **`players.preferred_location`**.

Оба поля — произвольный ввод пользователя, без нормализации на сервере (кроме `TrimSpace`).

### Время

Нигде не используется тип `TIMESTAMP` PostgreSQL для этих полей: даты и время пишутся в колонки **`TEXT`** строкой.

Формат везде один: **`time.RFC3339` в UTC**, то есть **`2006-01-02T15:04:05Z`** (с буквой **`Z`** или эквивалентным смещением). Его же возвращает API в JSON (`createdAt`, `updatedAt`, `joinedAt`, `startedAt`, `finishedAt` и т.д.).

Откуда берётся:

- **`created_at` / `updated_at`** у игрока и лобби — `time.Now().UTC().Format(time.RFC3339)` в момент операции.
- **`joined_at`** у участника лобби — то же при `join` (и при создании лобби хостом).
- **`started_at`** у лобби — выставляется, когда **два** игрока в лобби и оба **`ready`**.
- **`finished_at`** у лобби — когда оба нажали **`match-finished`** (casual) или при успешном **`ranked-result`** (ranked).

Поля вроде **`rankAttestedAt`** в админском **`PUT /admin/players/{id}/rank`** — **произвольная строка** из тела запроса; сервер не приводит её к RFC3339.

---

## 7.3) Каталог HTTP-ручек (сводка)

Ниже все маршруты, которые реально обрабатывает API (базовый путь в проде — ваш хост, локально обычно `http://localhost:8080`). Подробные тела запросов/ответов — в **п. 8** и **п. 9**.

### Публичные (без роли admin)

| Метод | Путь |
|--------|------|
| `GET` | `/health` |
| `POST` | `/auth/login` |
| `POST` | `/players` |
| `GET` | `/players/{id}` |
| `GET` | `/players/{id}/faction-experience` |
| `POST` | `/lobbies` |
| `GET` | `/lobbies/{id}` |
| `GET` | `/mission-conditions` |
| `POST` | `/lobbies/{id}/random-condition` |
| `POST` | `/lobbies/{id}/join` |
| `PUT` | `/lobbies/{id}/conditions` |
| `POST` | `/lobbies/{id}/ready` |
| `POST` | `/lobbies/{id}/match-finished` |
| `POST` | `/lobbies/{id}/ranked-result` |

Для **`join`**, **`ready`**, **`match-finished`**, **`ranked-result`** нужен заголовок **`Authorization: Bearer <JWT>`** и совпадение **`playerId`** в теле (где требуется) с токеном.

### Админские (JWT + роль `admin`)

| Метод | Путь |
|--------|------|
| `GET` | `/admin/players` |
| `PUT` | `/admin/players/{id}/rank` |
| `DELETE` | `/admin/players/{id}` |
| `DELETE` | `/admin/lobbies/{id}` |
| `DELETE` | `/admin/lobbies-history/{id}` |

---

## 8) Полный список API

Базовый URL в локальной среде: `http://localhost:8080`

Ошибки в общем случае: JSON **`{"error":"..."}`** и соответствующий HTTP-код (`400`, `401`, `403`, `404`, `409`, `500` и т.д.).

См. также **п. 7.2** (фракции, место, время).

### 8.1 Health

| | |
|---|---|
| Метод и путь | `GET /health` |
| Тело запроса | нет |
| Ответ `200` | `{"status":"ok"}` |

```json
{
  "status": "ok"
}
```

### 8.2 Регистрация игрока

| | |
|---|---|
| Метод и путь | `POST /players` |
| Успех | `201 Created` |
| Заголовки | `Content-Type: application/json` |

**Тело запроса** — только то, что клиент отправляет. Поле **`password`** нигде не возвращается.

```json
{
  "fullName": "Иван Петров",
  "nickname": "WolfGuard",
  "city": "Moscow",
  "contacts": "@wolfguard",
  "preferredLocation": "North club",
  "password": "supersecret123"
}
```

**Тело ответа при `201`** — это тот же объект профиля, что отдаёт **`GET /players/{id}`** для только что созданного `id` (в коде после вставки вызывается `getPlayerByID`). Значения **`createdAt`** / **`updatedAt`** — реальные метки времени в формате RFC3339 (в примере ниже условные).

Поля **`rankTitle`**, **`rankAttestedAt`**, **`collectionLink`** в JSON **нет**, пока они пустые в БД (`omitempty`).

```json
{
  "id": 1,
  "fullName": "Иван Петров",
  "nickname": "WolfGuard",
  "city": "Moscow",
  "contacts": "@wolfguard",
  "preferredLocation": "North club",
  "factions": [],
  "tournaments": [],
  "hobbyEvenings": [],
  "totalExperience": 0,
  "rating": 1500,
  "ratingRd": 350,
  "otherEvents": [],
  "factionExperience": [],
  "createdAt": "2026-03-28T12:00:00Z",
  "updatedAt": "2026-03-28T12:00:00Z"
}
```

### 8.3 Получить игрока

| | |
|---|---|
| Метод и путь | `GET /players/{id}` |
| Тело запроса | нет |

Ответ `200` — тот же состав полей, что в п. 8.2, плюс при наличии **`rankTitle`**, **`rankAttestedAt`**, **`collectionLink`**. Блок **`factionExperience`** подгружается из `player_faction_experience` (сортировка по имени фракции).

### 8.4 Логин

| | |
|---|---|
| Метод и путь | `POST /auth/login` |

Тело запроса:

```json
{
  "nickname": "WolfGuard",
  "password": "supersecret123"
}
```

Ответ `200`:

```json
{
  "token": "eyJhbGciOiJIUzI1NiIs...",
  "playerId": 1,
  "role": "player"
}
```

JWT живёт **24 часа** от момента выдачи (см. `internal/httpapi/auth.go`).

### 8.5 Создать лобби

| | |
|---|---|
| Метод и путь | `POST /lobbies` |

Тело (casual / ranked отличается только **`isRanked`**):

```json
{
  "hostPlayerId": 1,
  "player1": { "faction": "Clan Wolf" },
  "matchSize": 350,
  "isRanked": false
}
```

Ключ **`player1`** здесь потому, что **`hostPlayerId` равен 1**. Для хоста с id `7` используйте **`player7`**, и т.д.

Ответ `201` — объект лобби (как в п. 8.6). Обязательны **`hostPlayerId`**, **`player{hostPlayerId}.faction`** и **`matchSize`**.

### 8.6 Получить лобби

| | |
|---|---|
| Метод и путь | `GET /lobbies/{id}` |

Ответ `200` — пример для лобби с случайным условием миссии и двумя игроками. Каждый участник лежит под ключом **`player` + его `playerId`** (те же правила, что при создании и join). Массива **`players`** в JSON нет.

```json
{
  "id": 10,
  "hostPlayerId": 1,
  "player1": {
    "playerId": 1,
    "faction": "Clan Wolf",
    "isReady": true,
    "isFinished": false,
    "joinedAt": "2026-03-28T12:10:00Z"
  },
  "player2": {
    "playerId": 2,
    "faction": "Clan Jade Falcon",
    "isReady": true,
    "isFinished": false,
    "joinedAt": "2026-03-28T12:12:00Z"
  },
  "matchSize": 350,
  "isRanked": false,
  "missionConditionId": 3,
  "missionCondition": {
    "id": 3,
    "modeName": "Skirmish",
    "weatherName": "Rain",
    "description": "..."
  },
  "status": "started",
  "startedAt": "2026-03-28T12:15:00Z",
  "createdAt": "2026-03-28T12:10:00Z",
  "updatedAt": "2026-03-28T12:15:00Z"
}
```

Пока второй игрок не зашёл, объекта **`player2`** (или любого другого id второго участника) нет.

Если заданы кастомные условия, будут поля **`customMissionName`**, **`customWeatherName`**, **`customAtmosphereName`**, а **`missionConditionId`** может быть `null`.

### 8.7 Получить активные условия миссий

| | |
|---|---|
| Метод и путь | `GET /mission-conditions` |

Ответ `200` — JSON-массив (только **`is_active = true`**):

```json
[
  {
    "id": 1,
    "modeName": "Skirmish",
    "weatherName": "Clear",
    "description": ""
  }
]
```

### 8.8 Random условие для без-MMR лобби

| | |
|---|---|
| Метод и путь | `POST /lobbies/{id}/random-condition` |
| Тело запроса | нет |
| JWT | не требуется |

Выбирается **одна** случайная строка из `mission_conditions` (`ORDER BY random() LIMIT 1`). Ответ `200` — обновлённое лобби (как п. 8.6). Для **`isRanked=true`** вернётся ошибка.

### 8.9 Вход в лобби (игрок выбирает фракцию)

| | |
|---|---|
| Метод и путь | `POST /lobbies/{id}/join` |
| Заголовок | `Authorization: Bearer <token>` |

Тело ( **`playerId` должен совпадать с JWT** ). Ключ с фракцией **обязан совпадать с `playerId`**:

```json
{
  "playerId": 2,
  "player2": { "faction": "Clan Jade Falcon" }
}
```

Для игрока с id `5` используйте **`player5`**, и т.д. Лишние поля вида **`player3`**, если в теле **`playerId": 2`**, сервер отклонит.

Ответ `200` — полное лобби. В лобби не более **двух** участников.

### 8.10 Кастомные условия (только non-MMR)

| | |
|---|---|
| Метод и путь | `PUT /lobbies/{id}/conditions` |

Тело:

```json
{
  "missionName": "Capture Base",
  "weatherName": "Snow",
  "atmosphereName": "Thin"
}
```

Ответ `200` — лобби; **`mission_condition_id`** сбрасывается в `NULL`, заполняются три текстовых поля кастома. Для ranked — ошибка.

### 8.11 Кнопка «Готов»

| | |
|---|---|
| Метод и путь | `POST /lobbies/{id}/ready` |
| Заголовок | `Authorization: Bearer <token>` |

Тело:

```json
{
  "playerId": 1
}
```

Ответ `200` — лобби. Если **2** игрока и оба готовы — **`status`** станет **`started`**, выставится **`startedAt`**.

### 8.12 Кнопка «Матч завершен»

| | |
|---|---|
| Метод и путь | `POST /lobbies/{id}/match-finished` |
| Заголовок | `Authorization: Bearer <token>` |

Тело:

```json
{
  "playerId": 1
}
```

Ответ `200` — лобби. Когда **оба** игрока отметили завершение и матч ещё не был **`finished`**: **`status` → `finished`**, **`finishedAt`**, **`+1` total_experience**, обновление **`players.factions`** и **`player_faction_experience`**. Для ranked рейтинг через этот endpoint **не** меняется (см. **`ranked-result`**).

### 8.13 Получить опыт по фракциям (пагинация)

| | |
|---|---|
| Метод и путь | `GET /players/{id}/faction-experience` |

Query-параметры:
- **`sort`**: `exp_desc` (по умолчанию), `exp_asc`, `faction_asc`, `faction_desc`
- **`limit`**: 1..200, по умолчанию 50
- **`offset`**: ≥ 0, по умолчанию 0

Пример: `GET /players/1/faction-experience?sort=exp_desc&limit=20&offset=0`

Ответ `200`:

```json
{
  "playerId": 1,
  "sort": "exp_desc",
  "limit": 20,
  "offset": 0,
  "total": 3,
  "items": [
    { "faction": "Mercenaries", "experience": 8 },
    { "faction": "Clan Wolf", "experience": 5 },
    { "faction": "Clan Jade Falcon", "experience": 2 }
  ]
}
```

### 8.14 Зафиксировать результат ranked-матча (Glicko)

| | |
|---|---|
| Метод и путь | `POST /lobbies/{id}/ranked-result` |
| Заголовок | `Authorization: Bearer <token>` (должен быть участник лобби) |

Победа:

```json
{
  "winnerPlayerId": 1,
  "isDraw": false
}
```

Ничья:

```json
{
  "isDraw": true
}
```

Ответ `200` — лобби с **`status":"finished"`**, **`finishedAt`**, **`ratingApplied`** на стороне БД; у игроков обновятся **`rating`** и **`ratingRd`**. Повторный вызов для того же лобби — ошибка.

---

## 9) Админские API

Все методы ниже требуют:
- JWT с ролью `admin`
- заголовок `Authorization: Bearer <token>`

Без админской роли — **`403`** с `{"error":"forbidden"}`; без/с неверным токеном — **`401`** `{"error":"unauthorized"}`.

### 9.1 Список игроков (коллекция)

| | |
|---|---|
| Метод и путь | `GET /admin/players` |
| Тело запроса | нет |

Query-параметры (как у **`GET /players/{id}/faction-experience`**):

- **`sort`**: `id_asc` (по умолчанию), `id_desc`, `nickname_asc`, `nickname_desc`
- **`limit`**: 1..200, по умолчанию 50
- **`offset`**: ≥ 0, по умолчанию 0

Ответ **`200`** — краткие карточки (полный профиль по-прежнему через **`GET /players/{id}`**):

```json
{
  "sort": "id_asc",
  "limit": 50,
  "offset": 0,
  "total": 12,
  "items": [
    {
      "id": 1,
      "nickname": "WolfGuard",
      "fullName": "Иван Петров",
      "city": "Moscow",
      "role": "admin",
      "createdAt": "2026-03-28T12:00:00Z"
    }
  ]
}
```

### 9.2 Установить звание игроку

| | |
|---|---|
| Метод и путь | `PUT /admin/players/{id}/rank` |

Тело запроса (оба поля обязательны, произвольные строки):

```json
{
  "rankTitle": "Captain",
  "rankAttestedAt": "2026-03-24"
}
```

Ответ `200` — полный объект игрока (как **`GET /players/{id}`**).

### 9.3 Удалить лобби и его историю

| | |
|---|---|
| Метод и путь | `DELETE /admin/lobbies/{id}` |
| Тело запроса | нет |

Ответ **`204`**. В теле при этом всё равно может уйти небольшой JSON `{"status":"deleted"}` (особенность текущей реализации `writeJSON`).

Удаляются строки в **`lobbies_history`** с **`original_lobby_id`**, затем само лобби (каскадно зачистятся **`lobby_players`** и связанное).

### 9.4 Удалить конкретную запись из истории лобби

| | |
|---|---|
| Метод и путь | `DELETE /admin/lobbies-history/{id}` |

Ответ **`204`** (и при необходимости тот же JSON `status`, как при удалении лобби в п. 9.3).

### 9.5 Удалить игрока

| | |
|---|---|
| Метод и путь | `DELETE /admin/players/{id}` |

Ответ **`204`**. Перед удалением игрока API удаляет его записи в **`lobbies_history`** и **`lobbies`** где он хост; дальше срабатывает **`DELETE FROM players`** (остальное — каскады FK в БД).

---

## 10) Сценарии для фронтенда

### 10.1 Обычный flow для игрока

1. `POST /players` - регистрация
2. `POST /auth/login` - логин и получение токена
3. `POST /lobbies` - создание лобби
4. Второй игрок: `POST /lobbies/{id}/join` с телом **`playerId` + `player{playerId}.faction`**
5. Для `isRanked=false`: либо `POST /lobbies/{id}/random-condition`, либо `PUT /lobbies/{id}/conditions`
6. Оба игрока: `POST /lobbies/{id}/ready`
7. После матча оба игрока: `POST /lobbies/{id}/match-finished`
8. `GET /lobbies/{id}` и `GET /players/{id}/faction-experience` для обновления UI статистики

### 10.2 Админский flow

1. Логин админа (`POST /auth/login`)
2. `GET /admin/players` — список игроков (пагинация/сортировка)
3. `PUT /admin/players/{id}/rank` - выдать звание
4. `DELETE /admin/lobbies/{id}` - удалить лобби/историю
5. `DELETE /admin/players/{id}` - удалить игрока

---

## 11) Тесты

Интеграционные тесты лежат в пакете `internal/httpapi` (`api_test.go`). Пакеты `cmd/*` и `internal/db` тестов не содержат — при `go test ./...` для них выводится `[no test files]`.

### 11.1 Запуск

```bash
go test ./...
```

С подробным логом по каждому тесту:

```bash
go test ./... -v
```

### 11.2 Тестовая БД

Без переменной **`TEST_DATABASE_URL`** все тесты из `internal/httpapi` **пропускаются** (`SKIP`), пакет при этом помечается как `PASS` — реальная проверка API не выполняется.

Задайте строку подключения к PostgreSQL (удобно отдельная БД, чтобы не трогать рабочие данные):

```powershell
$env:TEST_DATABASE_URL="postgres://postgres:postgres@localhost:5432/solaris_matchmaking_test?sslmode=disable"
go test ./...
```

Если в этой же сессии уже задан `DATABASE_URL`, можно скопировать его:

```powershell
$env:TEST_DATABASE_URL = $env:DATABASE_URL
go test ./... -v
```

В **cmd** переменная задаётся так: `set TEST_DATABASE_URL=postgres://...` (без кавычек вокруг всей строки, если в пароле есть спецсимволы — используйте URL-кодирование, например `@` → `%40`).

Сохранить вывод в файл (PowerShell, текущая папка — корень репозитория):

```powershell
go test ./... -v 2>&1 | Tee-Object -FilePath test-results.txt
```

В **cmd** одновременно экран и файл без PowerShell не получится; только в файл: `go test ./... -v > test-results.txt 2>&1`.

### 11.3 Список тестов (`internal/httpapi`)

| Тест | Что проверяет |
|------|----------------|
| `TestHealthEndpoint` | `GET /health` возвращает успех |
| `TestRegisterPlayerAndCreateLobbyFlow` | регистрация игрока и создание лобби хостом |
| `TestCreateLobbyFailsForUnknownHostPlayer` | создание лобби с несуществующим `hostPlayerId` отклоняется |
| `TestMissionConditionsOnlyForCasualLobbies` | условия миссий / random-condition только для лобби без MMR (`isRanked=false`) |
| `TestAdminSetRankAndDeleteLobbyAndPlayer` | админ: список игроков `GET /admin/players`, звание, удаление лобби, удаление игрока |
| `TestNonAdminCannotCallAdminEndpoints` | игрок без роли `admin` не может вызывать админские маршруты (в т.ч. список игроков) |
| `TestCasualLobbyReadyAndFinishGivesExperience` | casual: `join` → `ready` → оба `match-finished` → начисление опыта и `player_faction_experience` |
| `TestGetPlayerFactionExperienceWithPaginationAndSort` | `GET /players/{id}/faction-experience` с сортировкой и пагинацией |
| `TestRankedResultAppliesGlickoOnce` | ranked: `ranked-result` обновляет Glicko один раз, повторный вызов не дублирует расчёт |

---

## 12) Частые проблемы

- `database init failed`:
  - проверь `DATABASE_URL`
  - проверь доступность PostgreSQL
- `invalid credentials`:
  - неверный `nickname/password`
  - игрок не зарегистрирован
- `forbidden` на админ endpoint:
  - токен не admin
  - проверь роль через `go run ./cmd/admin list-admins`
- random-condition не работает:
  - лобби ranked (`isRanked=true`)
  - нет активных записей в `mission_conditions`
- `join/ready/match-finished` возвращают `401/403`:
  - отсутствует `Authorization: Bearer <token>`
  - `playerId` в body не совпадает с `playerId` из JWT

---

## 13) Что можно развивать дальше

- refresh tokens и ротация JWT
- ограничение/валидация `matchSize` по правилам игры
- полноценный CRUD для `mission_conditions` из админ-панели
- пагинация/фильтрация списка лобби и игроков
- аудит-лог админских действий
