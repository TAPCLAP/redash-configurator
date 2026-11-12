# redash-configurator

[English](README.md)

Конфигуратор приводит MySQL data source в Redash к переменным среды. По расписанию он создаёт недостающие source, обновляет параметры подключения, проверяет, что сохранённый пароль ещё подходит, и выдаёт группам full access или view only.

Он ходит в HTTP API Redash от имени администратора. Redash он не устанавливает и к MySQL сам не подключается. Путь в `SOURCE_<ID>_SSL_CACERT` — это путь на сервере Redash.

## Пример

`EU_AUTH` — только имя группы переменных. Имя data source в Redash задаёт `SOURCE_EU_AUTH_NAME`.

```bash
REDASH_URL=https://redash.example.com
REDASH_API_KEY=admin-user-api-key
SYNC_INTERVAL=5m

SOURCE_EU_AUTH_NAME=auth-eu
SOURCE_EU_AUTH_HOST=db-eu.internal
SOURCE_EU_AUTH_PORT=3306
SOURCE_EU_AUTH_USER=redash
SOURCE_EU_AUTH_PASSWORD=secret
SOURCE_EU_AUTH_DBNAME=auth
SOURCE_EU_AUTH_DBTYPE=mysql
SOURCE_EU_AUTH_SSL_CACERT=/etc/ssl/certs/ca.pem
SOURCE_EU_AUTH_WRITE_GROUPS=auth-writers
SOURCE_EU_AUTH_READ_GROUPS=auth-readers
```

На каждом проходе MySQL `use_ssl` выставляется в true, а `ssl_mode` — в `required`. Группа `auth-writers` может выполнять и редактировать запросы. `auth-readers` может только смотреть. Если группы ещё нет, она создаётся. Второй source — это ещё один набор `SOURCE_<ID>_NAME`. Отдельного списка source нет.

Поля, проверка пароля и пример Deployment описаны в [использовании конфигуратора](docs/usage.ru.md).

## Запуск

Тег `v*` собирает `linux/amd64` и `linux/arm64` и публикует `ghcr.io/<owner>/<repo>:<tag>`. Запускайте одну реплику. Две реплики будут повторять одни и те же вызовы API.

```bash
docker run --rm \
  -e REDASH_URL \
  -e REDASH_API_KEY \
  -e SOURCE_EU_AUTH_NAME \
  -e SOURCE_EU_AUTH_HOST \
  -e SOURCE_EU_AUTH_PORT \
  -e SOURCE_EU_AUTH_USER \
  -e SOURCE_EU_AUTH_PASSWORD \
  -e SOURCE_EU_AUTH_DBNAME \
  -e SOURCE_EU_AUTH_DBTYPE \
  ghcr.io/tapclap/redash-configurator:v0.1.0
```

Неверная конфигурация сразу завершает процесс с ненулевым кодом. Ошибка API или подключения позже пишется в лог, и процесс ждёт следующего интервала.

## Документация

- [Использование конфигуратора](docs/usage.ru.md)

## Разработка

```bash
make test
make lint
make docker-build IMG=redash-configurator:latest
```

Go 1.25. Остальные цели перечисляет `make help`.
