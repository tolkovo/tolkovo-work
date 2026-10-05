# Развёртывание Tolkovo

Инструкция для того, кто запускает Tolkovo на своём сервере. Здесь описано, что собрать, что запустить, что открыть наружу, что бэкапить и как обновлять.

Файлы:

| Файл | Что это |
|---|---|
| [`../Dockerfile`](../Dockerfile) | Один образ для всех ролей: сервер на Go и собранная панель внутри |
| [`production/docker-compose.yml`](production/docker-compose.yml) | Пример для одного хоста: приложение, PostgreSQL, Valkey |
| [`production/.env.example`](production/.env.example) | Образ и пароли БД (читает compose) |
| [`production/tolkovo.env.example`](production/tolkovo.env.example) | Настройки приложения |
| [`compose/.env.example`](compose/.env.example) | Полный список переменных `TOLKOVO_*` с пояснениями |
| [`compose/initdb/01-app-role.sh`](compose/initdb/01-app-role.sh) | Создание ролей БД при первом запуске PostgreSQL |

Compose — это пример. Kubernetes, Nomad, свой systemd или управляемые PostgreSQL и Redis подходят так же: ниже описано, что нужно приложению, независимо от способа запуска.

## 1. Что запускается

```
              HTTPS, один хост (support.example.com)
браузер ───►  обратный прокси (TLS)  ───►  tolkovo serve --role=all  :8080
виджет                                       │  панель, REST, 2 WebSocket,
Telegram                                     │  вебхуки, фоновые задачи
                                             ├──► PostgreSQL 16
                                             ├──► Redis / Valkey
                                             └──► файлы: том или S3
```

- **Образ один.** Внутри бинарник `tolkovo` (миграции встроены) и статика панели. Запуск от непривилегированного пользователя (uid 65532), без shell (distroless).
- **Команды:**
  - `serve --role=all|api|realtime|worker` — сервер;
  - `migrate up` — миграции, от владельца таблиц;
  - `keygen` — новый мастер-ключ;
  - `healthcheck` — проверка для Docker;
  - `version`.
- **Зависимости:**
  - PostgreSQL 16, расширение `citext` (ставится миграцией);
  - Redis 7+ или Valkey 8: сессии, лимиты, рассылка событий realtime. Данные в нём не главные, но при потере все выходят из панели;
  - файлы: том на диске (драйвер `fs`, только одна копия приложения) или S3-совместимое хранилище (драйвер `s3`).
- **Хостинг — только в ЕС** (решение владельца, [docs/README.md §1](../docs/README.md)). Это касается сервера, базы, бэкапов и S3.
- **Минимум:** 2 vCPU, 4 ГБ RAM на всё вместе с PostgreSQL. Диск — по объёму файлов: вложения до 25 МБ, отчёты приложений до 50 МБ.

## 2. Сборка образа

Из корня репозитория:

```sh
docker build --build-arg VERSION=0.1.0 -t registry.example.com/tolkovo:0.1.0 .
```

- `VERSION` попадает в `tolkovo version` и в ответ `/healthz`.
- Сборка ставит зависимости панели из `pnpm-lock.yaml`, собирает её Vite и собирает Go-бинарник. Сеть нужна только на этапе сборки: npm, Go-модули, базовые образы с Docker Hub и `gcr.io`.
- Под другую архитектуру:

  ```sh
  docker buildx build --platform linux/amd64,linux/arm64 --push -t registry.example.com/tolkovo:0.1.0 .
  ```

  Панель и Go собираются на платформе сборщика, без эмуляции, так что это быстро.
- Публичного образа в GHCR пока нет: кладите образ в свой реестр или собирайте прямо на сервере. Имя образа в compose задаёт `TOLKOVO_IMAGE` в `.env`.

## 3. Запуск по compose

```sh
cd deploy/production
cp .env.example .env
cp tolkovo.env.example tolkovo.env
```

1. В `.env` задайте образ и три пароля БД. Пароли берите в hex (`openssl rand -hex 24`): они подставляются в URL без экранирования.
2. В `tolkovo.env` обязательно задайте:
   - `TOLKOVO_PUBLIC_URL` — https-адрес, который открывают люди;
   - `TOLKOVO_MASTER_KEY` — `docker run --rm <образ> keygen`;
   - `TOLKOVO_STORAGE_SIGNING_KEY` — `openssl rand -hex 32`;
   - `TOLKOVO_TRUSTED_PROXIES` — адрес прокси (см. §4).

   Рекомендуется SMTP: без него приглашения передаются ссылкой вручную, а сброс пароля по почте не работает.
3. `docker compose up -d`. Порядок запуска:
   1. PostgreSQL при первом старте создаёт роли (`initdb`);
   2. `migrate` применяет миграции и завершается;
   3. только после этого стартует `app`.
4. Проверка:

   ```sh
   docker compose ps                        # app — healthy, migrate — exited (0)
   curl -s http://127.0.0.1:8080/readyz     # {"status":"ready"} — база и Redis отвечают
   ```

5. Откройте `TOLKOVO_PUBLIC_URL` и зарегистрируйтесь. Первый аккаунт становится владельцем своего пространства. Остальных приглашают из «Настройки → Команда».

> **Регистрация открыта.** Сейчас любой, кто откроет адрес, может создать своё отдельное пространство. Чужих данных он не увидит, но будет пользоваться вашим сервером. Пока регистрацию не закрывает настройка, ограничьте доступ к `POST /api/v1/auth/register` на прокси после первой регистрации, например по IP.

Приложение при неверных настройках не стартует и пишет в лог, какой переменной не хватает: `docker compose logs app`.

## 4. Обратный прокси

Прокси отвечает за TLS и пересылает всё на `127.0.0.1:8080`.

**Один хост.** Панель, REST API и оба WebSocket работают на одном адресе. Cookie сессии `__Host-` привязана к хосту, поэтому отдельные хосты для `api.` и `ws.` сломают вход.

Требования:

| Что | Почему |
|---|---|
| Upgrade до WebSocket на `/api/v1/realtime` (панель) и `/api/v1/widget/realtime` (виджет) | Без него нет живых обновлений, а виджет не узнаёт об ответах сразу |
| Тайм-аут простоя WebSocket не меньше 1 часа | Иначе соединения рвутся и переподключаются |
| Тело запроса до 60 МБ | Файлы драйвера `fs` идут через приложение (`/api/v1/files`): вложение до 25 МБ, файл отчёта до 25 МБ |
| `X-Forwarded-For`, адрес прокси в `TOLKOVO_TRUSTED_PROXIES` | Лимиты считаются по IP посетителя. Без этого все посетители делят лимит прокси, и под нагрузкой начнутся 429 |
| Заголовок `Host` без изменений или совпадающий с `TOLKOVO_PUBLIC_URL` | Проверка Origin для изменяющих запросов и WebSocket |
| HSTS | Сервер его не ставит, это задача прокси |
| Снаружи доступны `/hooks/telegram/*` и `/hooks/github/*` | Вебхуки Telegram и GitHub. Telegram требует валидный HTTPS |
| `/healthz`, `/readyz` наружу открывать не обязательно | Они нужны мониторингу |

Пример Caddy (сам получает сертификат Let's Encrypt):

```
support.example.com {
	request_body {
		max_size 60MB
	}
	reverse_proxy 127.0.0.1:8080
}
```

Caddy проксирует WebSocket и ставит `X-Forwarded-For` без дополнительных настроек.

Пример nginx:

```nginx
map $http_upgrade $connection_upgrade { default upgrade; '' close; }

server {
    listen 443 ssl;
    http2 on;
    server_name support.example.com;
    # ssl_certificate ...; ssl_certificate_key ...;
    add_header Strict-Transport-Security "max-age=31536000" always;
    client_max_body_size 60m;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
```

**`TOLKOVO_TRUSTED_PROXIES`.** Прокси на том же хосте приходит в контейнер с адреса шлюза Docker. Узнать его можно командой `docker network inspect tolkovo_default` (поле `Gateway`), обычно это `172.x.0.1`. Прокси в compose-сети указывается подсетью сети. Значение `0.0.0.0/0` запрещено: так любой подделает свой IP.

## 5. Свои PostgreSQL и Redis

Для управляемой базы или уже существующего кластера уберите сервисы `postgres` и `redis` из compose и задайте URL в `tolkovo.env`. Строки `environment:` в compose тоже уберите: они перекрывают `env_file`.

Нужны три роли. Миграции создают группы `tolkovo_app` и `tolkovo_system`, если их нет, и выдают им права на таблицы. Входы создаются заранее:

```sql
-- владелец таблиц: только для migrate
CREATE ROLE tolkovo LOGIN PASSWORD '...';
CREATE DATABASE tolkovo OWNER tolkovo;
-- группы и входы приложения и системной роли
CREATE ROLE tolkovo_app NOLOGIN;
CREATE ROLE tolkovo_app_login LOGIN PASSWORD '...' IN ROLE tolkovo_app;
CREATE ROLE tolkovo_system NOLOGIN;
CREATE ROLE tolkovo_system_login LOGIN PASSWORD '...' IN ROLE tolkovo_system;
```

| Переменная | Роль | Кто использует |
|---|---|---|
| `TOLKOVO_MIGRATE_DATABASE_URL` | владелец таблиц | только `migrate up` |
| `TOLKOVO_DATABASE_URL` | `tolkovo_app_login` | все роли сервера. Не владелец таблиц, без `BYPASSRLS`: на него действует RLS, изоляция пространств держится на этом |
| `TOLKOVO_SYSTEM_DATABASE_URL` | `tolkovo_system_login` | роли `worker` и `all`: ретрансляция событий и очистка, доступ между пространствами только по явным политикам |

Важно:

- `migrate` загружает всю конфигурацию, поэтому ему нужен тот же `tolkovo.env`, что и приложению.
- **PgBouncer** — в режиме `session`: фоновые задачи (River) слушают `LISTEN/NOTIFY`, а работу в режиме `transaction` мы не проверяли.
- **TLS к базе:** `?sslmode=verify-full` в URL.
- **Redis:** `redis://` или `rediss://` (TLS) в `TOLKOVO_REDIS_URL`. Номер базы можно указать в URL. Redis Cluster не поддерживается.

## 6. Роли процесса и несколько копий

`--role=all` — всё в одном процессе. Этого хватает для одного сервера.

Если нужно разнести нагрузку, используется тот же образ с разными ролями:

| Роль | Что делает | Копий |
|---|---|---|
| `api` | REST, вебхуки, файлы `fs`, панель | сколько угодно |
| `realtime` | WebSocket панели и виджета | сколько угодно, события между копиями идут через Redis |
| `worker` | фоновые задачи: отправка ответов в каналы, письма, обработка отчётов, ретрансляция событий, очистка | 1 или больше (задачи берутся из очереди в PostgreSQL) |

При разделении ролей:

- Прокси направляет `/api/v1/realtime` и `/api/v1/widget/realtime` на `realtime`, всё остальное — на `api`.
- Несколько копий `api` требуют `TOLKOVO_STORAGE_DRIVER=s3`: файлы `fs` лежат на диске одной копии.
- У `worker` нет HTTP. В compose для него отключите healthcheck (`healthcheck: { disable: true }`).

Остановка: по SIGTERM сервер дорабатывает запросы и закрывает WebSocket кодом 1012, клиенты переподключаются сами. Срок задаёт `TOLKOVO_SHUTDOWN_TIMEOUT` (20 с). Давайте контейнеру чуть больше: `stop_grace_period: 30s`.

## 7. Обновление

```sh
# в .env: TOLKOVO_IMAGE=registry.example.com/tolkovo:0.2.0
docker compose pull      # или соберите образ на сервере
docker compose up -d     # migrate применит новые миграции, затем перезапустится app
```

- Миграции пишутся по правилу «expand → migrate → contract»: старая версия приложения работает на новой схеме, пока идёт выкладка.
- Откат: верните прежний образ. Схему назад не откатывайте: `migrate down` нужен только для разработки и может удалить данные.
- Перед обновлением сделайте бэкап базы (§8).

## 8. Бэкапы

| Что | Как | Без этого |
|---|---|---|
| PostgreSQL | `pg_dump -Fc` по расписанию, хранить в ЕС | Потеря всех диалогов и настроек |
| Файлы | Том `files` (restic, rsync) или версионирование бакета S3 | Вложения и отчёты не открываются |
| `TOLKOVO_MASTER_KEY` | Отдельно от бэкапов базы, например в менеджере секретов | Токены ботов, сессии Telegram, ключи ИИ и GitHub из бэкапа не расшифровать |
| `TOLKOVO_STORAGE_SIGNING_KEY` | Вместе с остальными секретами | После смены перестают работать ранее выданные ссылки на файлы, на срок до `URL_TTL` |
| Redis | Не обязателен | Все выйдут из панели и войдут заново |

Пример дампа из compose:

```sh
docker compose exec -T postgres pg_dump -U tolkovo -Fc tolkovo > tolkovo-$(date +%F).dump
```

Восстановление проверяйте на отдельной машине хотя бы раз до запуска в работу.

## 9. Мониторинг и логи

- `GET /healthz` — процесс жив: `{"status":"ok","version":"..."}`.
- `GET /readyz` — база и Redis отвечают, иначе 503. Используйте для балансировщика и readiness-проверки.
- Логи — JSON в stdout, по строке на событие: `request_id`, путь, статус, длительность. Тексты сообщений, токены и пароли в лог не пишутся. Уровень задаёт `TOLKOVO_LOG_LEVEL`.
- Что стоит отслеживать:
  - 5xx в логах прокси;
  - `/readyz`;
  - место на диске под томами `pg-data` и `files`;
  - в логах — `level=ERROR` фоновых задач: доставка в Telegram, письма.

Метрик Prometheus (`/metrics`) пока нет.

## 10. Безопасность

- Наружу открыт только прокси (443, а 80 для выпуска сертификата). Порт 8080 слушается только на `127.0.0.1`. PostgreSQL и Redis в примере наружу не открыты.
- Файлы `.env` и `tolkovo.env` содержат секреты: права `600`, в git не класть (уже в `.gitignore`).
- Мастер-ключ меняется без простоя:
  1. новый ключ ставится в `TOLKOVO_MASTER_KEY` с бо́льшей версией;
  2. старый переносится в `TOLKOVO_MASTER_KEYS_RETIRED` как `версия:ключ`;
  3. когда сохранённые значения перешифрованы, старый ключ можно убрать.
- Запросы к адресам из настроек (вебхуки, адреса ИИ-провайдеров) не ходят в частные сети. Исключение для локальных моделей — `TOLKOVO_EGRESS_ALLOW_PRIVATE`.
