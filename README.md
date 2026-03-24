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

Для `isRanked=true` используется базовый Glicko-1 (2 игрока в матче):

- стартовые значения игрока:
  - `rating = 1500`
  - `ratingRD = 350`
- после ranked-результата обновляются:
  - `players.rating`
  - `players.rating_rd`
- в `rating_history` пишется запись изменения.

Результат для ranked подтверждается endpoint-ом:
- `POST /lobbies/{id}/ranked-result`

Тело:

```json
{
  "winnerPlayerId": 1,
  "isDraw": false
}
```

Или ничья:

```json
{
  "isDraw": true
}
```

Ограничения:
- только для ranked лобби
- вызывать может только участник лобби (JWT)
- расчет рейтинга можно применить только один раз (`rating_applied`)

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

### 11.1 Запуск

```bash
go test ./...
```

### 11.2 Тестовая БД

Нужна отдельная PostgreSQL БД и переменная:

```powershell
$env:TEST_DATABASE_URL="postgres://postgres:postgres@localhost:5432/solaris_matchmaking_test?sslmode=disable"
go test ./...
```

### 11.3 Что покрыто

- health endpoint
- регистрация + базовый flow лобби
- негативные кейсы создания лобби
- логика миссий для без-MMR
- flow non-MMR: `join -> conditions -> ready -> match-finished`
- проверка раздельного опыта по фракциям
- endpoint статистики по фракциям с сортировкой/пагинацией
- ranked Glicko обновление рейтинга + защита от повторного применения
- админский доступ и ограничения ролей
- админские операции удаления/изменения

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
