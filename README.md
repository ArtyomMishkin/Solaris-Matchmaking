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

## 8) Полный список API

Базовый URL в локальной среде: `http://localhost:8080`

### 8.1 Health

- `GET /health`

Ответ:

```json
{
  "status": "ok"
}
```

### 8.2 Регистрация игрока

- `POST /players`

Request:

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

### 8.3 Получить игрока

- `GET /players/{id}`

### 8.4 Логин

- `POST /auth/login`

Request:

```json
{
  "nickname": "WolfGuard",
  "password": "supersecret123"
}
```

Response:

```json
{
  "token": "jwt-token",
  "playerId": 1,
  "role": "admin"
}
```

### 8.5 Создать лобби

- `POST /lobbies`

Request (без MMR):

```json
{
  "hostPlayerId": 1,
  "faction": "Clan Wolf",
  "matchSize": 350,
  "isRanked": false
}
```

Request (MMR):

```json
{
  "hostPlayerId": 1,
  "faction": "Clan Wolf",
  "matchSize": 350,
  "isRanked": true
}
```

### 8.6 Получить лобби

- `GET /lobbies/{id}`

### 8.7 Получить активные условия миссий

- `GET /mission-conditions`

### 8.8 Random условие для без-MMR лобби

- `POST /lobbies/{id}/random-condition`

### 8.9 Вход в лобби (игрок выбирает фракцию)

- `POST /lobbies/{id}/join`
- Требуется JWT игрока (`Authorization: Bearer <token>`)
- `playerId` в body должен совпадать с `playerId` из JWT

Request:

```json
{
  "playerId": 2,
  "faction": "Clan Jade Falcon"
}
```

### 8.10 Кастомные условия (только non-MMR)

- `PUT /lobbies/{id}/conditions`
- Для `isRanked=true` вернется ошибка

Request:

```json
{
  "missionName": "Capture Base",
  "weatherName": "Snow",
  "atmosphereName": "Thin"
}
```

### 8.11 Кнопка "Готов"

- `POST /lobbies/{id}/ready`
- Требуется JWT игрока (`Authorization: Bearer <token>`)
- `playerId` в body должен совпадать с `playerId` из JWT

Request:

```json
{
  "playerId": 1
}
```

Если в лобби ровно 2 игрока и оба готовы, статус лобби автоматически меняется на `started`.

### 8.12 Кнопка "Матч завершен"

- `POST /lobbies/{id}/match-finished`
- Требуется JWT игрока (`Authorization: Bearer <token>`)
- `playerId` в body должен совпадать с `playerId` из JWT

Request:

```json
{
  "playerId": 1
}
```

Когда оба игрока в лобби подтвердили завершение:
- статус лобби -> `finished`
- оба игрока получают `+1` к `total_experience`
- выбранные при входе в лобби фракции добавляются в `players.factions` (если их там еще не было)
- опыт также начисляется по фракциям в отдельной таблице `player_faction_experience`

### 8.13 Получить опыт по фракциям

- `GET /players/{id}/faction-experience`

Query-параметры:
- `sort`:
  - `exp_desc` (по умолчанию)
  - `exp_asc`
  - `faction_asc`
  - `faction_desc`
- `limit` (1..200, по умолчанию 50)
- `offset` (>= 0, по умолчанию 0)

Пример:

`GET /players/1/faction-experience?sort=exp_desc&limit=20&offset=0`

Пример ответа:

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

- `POST /lobbies/{id}/ranked-result`
- Требуется JWT участника лобби (`Authorization: Bearer <token>`)
- Работает только для `isRanked=true`
- Рейтинг пересчитывается один раз

---

## 9) Админские API

Все методы ниже требуют:
- JWT с ролью `admin`
- заголовок `Authorization: Bearer <token>`

### 9.1 Установить звание игроку

- `PUT /admin/players/{id}/rank`

Request:

```json
{
  "rankTitle": "Captain",
  "rankAttestedAt": "2026-03-24"
}
```

### 9.2 Удалить лобби и его историю

- `DELETE /admin/lobbies/{id}`

### 9.3 Удалить конкретную запись из истории лобби

- `DELETE /admin/lobbies-history/{id}`

### 9.4 Удалить игрока

- `DELETE /admin/players/{id}`

Удаляются также связанные лобби/история (через логику API и каскадные связи).

---

## 10) Сценарии для фронтенда

### 10.1 Обычный flow для игрока

1. `POST /players` - регистрация
2. `POST /auth/login` - логин и получение токена
3. `POST /lobbies` - создание лобби
4. Второй игрок: `POST /lobbies/{id}/join` (с выбором фракции)
5. Для `isRanked=false`: либо `POST /lobbies/{id}/random-condition`, либо `PUT /lobbies/{id}/conditions`
6. Оба игрока: `POST /lobbies/{id}/ready`
7. После матча оба игрока: `POST /lobbies/{id}/match-finished`
8. `GET /lobbies/{id}` и `GET /players/{id}/faction-experience` для обновления UI статистики

### 10.2 Админский flow

1. Логин админа (`POST /auth/login`)
2. `PUT /admin/players/{id}/rank` - выдать звание
3. `DELETE /admin/lobbies/{id}` - удалить лобби/историю
4. `DELETE /admin/players/{id}` - удалить игрока

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
| `TestAdminSetRankAndDeleteLobbyAndPlayer` | админ: звание игрока, удаление лобби, удаление игрока |
| `TestNonAdminCannotCallAdminEndpoints` | игрок без роли `admin` не может вызывать админские маршруты |
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
