# Использование конфигуратора

[English](usage.md)

Процесс читает окружение, приводит Redash к нему, спит `SYNC_INTERVAL` и повторяет это до SIGINT или SIGTERM. Первый проход начинается сразу. Сбой одного source не останавливает остальные. Пароли в лог не пишутся.

API key должен принадлежать администратору. Redash принимает его как `Authorization: Key <api-key>`. Создание и изменение data source, проверка подключения и управление группами — админские методы.

## Переменные среды

| Переменная | Обязательна | Смысл |
|---|---|---|
| `REDASH_URL` | да | Абсолютный `http` или `https` URL Redash. Хвостовой слэш отбрасывается |
| `REDASH_API_KEY` | да | API key пользователя-администратора |
| `SYNC_INTERVAL` | нет | Go duration, по умолчанию `5m`. Подходят `30s` и `1h`. Ноль и отрицательные значения отклоняются |
| `SOURCE_<ID>_NAME` | да | Имя data source в Redash. Source обнаруживается по этому ключу |
| `SOURCE_<ID>_HOST` | да | Хост MySQL |
| `SOURCE_<ID>_PORT` | да | TCP-порт от 1 до 65535 |
| `SOURCE_<ID>_USER` | да | Пользователь MySQL |
| `SOURCE_<ID>_PASSWORD` | да | Пароль MySQL. Пробелы по краям сохраняются |
| `SOURCE_<ID>_DBNAME` | да | Имя базы. В Redash уходит как `db` |
| `SOURCE_<ID>_DBTYPE` | да | Пока только `mysql`. Регистр не важен |
| `SOURCE_<ID>_SSL_CACERT` | нет | Путь к CA-файлу на сервере Redash. По умолчанию пусто |
| `SOURCE_<ID>_WRITE_GROUPS` | нет | Имена групп через запятую с full access |
| `SOURCE_<ID>_READ_GROUPS` | нет | Имена групп через запятую с view only |

`<ID>` соответствует `[A-Z0-9_]+`. У `SOURCE_EU_AUTH_NAME` id равен `EU_AUTH`. Ключ с суффиксом `_DBNAME` — это имя базы, а не ещё один source. Два id с одинаковым `NAME` — ошибка старта. Группа сразу в write и в read — тоже. Ноль source — тоже ошибка старта. Имена, хосты, пользователи, имена баз и пути CA обрезаются по краям. Имена групп обрезаются, пустые элементы выбрасываются, повторы внутри одного списка игнорируются.

Эти параметры отправляются всегда и из окружения не читаются:

- `use_ssl`: `true`
- `ssl_mode`: `required`

Пароль Redash хранит в поле `passwd`. Пустой путь CA не отправляется. Остальные уже сохранённые параметры MySQL, например `charset`, при обновлении source остаются на месте.

Source, которые пропали из окружения, в Redash не удаляются.

## Что делает один проход

1. Читает группы. Создаёт недостающие группы из write и read. Новая группа получает права по умолчанию Redash. Пользователей в группы конфигуратор не добавляет.
2. Ищет каждый data source по точному `NAME`.
3. Создаёт его или обновляет, если разошлись host, port, user, база, режим SSL, путь CA или тип. Пароль из окружения уходит вместе с этими полями.
4. Если эти поля уже совпадают, вызывает `POST /api/data_sources/<id>/test`.
5. Приводит связи с группами к спискам.

Пароль и его хеш API не возвращает. `GET /api/data_sources/<id>` подставляет вместо `passwd` значение `--------`. Сравнить эту маску с окружением нельзя. Проверка — тест подключения:

- `ok: true` — сохранённый пароль подходит. Он не перезаписывается.
- `ok: false` и в сообщении есть `1045` или `Access denied` — MySQL отклонил пароль. Конфигуратор записывает пароль из окружения и тестирует ещё раз.
- Любая другая ошибка, например отказ в соединении или неизвестная база, не доказывает, что пароль неверный. Пароль остаётся как был, сообщение пишется в лог.

Тест также выполняется после каждого создания и обновления. HTTP-клиент ждёт до двух минут: Redash отвечает только когда завершится его задача проверки.

При создании data source Redash сам привязывает builtin-группу `default` с full access. Builtin-группы (`default`, `admin`), которых нет в списках, не меняются. Обычная группа, которой больше нет ни в одном списке, отвязывается от source. Сама группа не удаляется. Builtin-группа, названная в списке, получает доступ этого списка.

Full access — это `view_only: false`. View only — `view_only: true`. Новую связь Redash создаёт как full access, поэтому read-группа сначала добавляется, а затем переключается в view only.

Если в Redash уже есть два data source с одним управляемым именем или две группы с одним нужным именем, этот source или этот проход завершается ошибкой и повторяется на следующем интервале.

## Deployment

Одна реплика. Контейнер не слушает порт. CA-файл сюда монтировать не нужно: его читает Redash со своей файловой системы.

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: redash-configurator
stringData:
  api-key: replace-me
  eu-auth-password: replace-me
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: redash-configurator
spec:
  replicas: 1
  selector:
    matchLabels:
      app: redash-configurator
  template:
    metadata:
      labels:
        app: redash-configurator
    spec:
      containers:
        - name: redash-configurator
          image: ghcr.io/tapclap/redash-configurator:v0.1.0
          env:
            - name: REDASH_URL
              value: https://redash.example.com
            - name: REDASH_API_KEY
              valueFrom:
                secretKeyRef:
                  name: redash-configurator
                  key: api-key
            - name: SYNC_INTERVAL
              value: 5m
            - name: SOURCE_EU_AUTH_NAME
              value: auth-eu
            - name: SOURCE_EU_AUTH_HOST
              value: db-eu.internal
            - name: SOURCE_EU_AUTH_PORT
              value: "3306"
            - name: SOURCE_EU_AUTH_USER
              value: redash
            - name: SOURCE_EU_AUTH_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: redash-configurator
                  key: eu-auth-password
            - name: SOURCE_EU_AUTH_DBNAME
              value: auth
            - name: SOURCE_EU_AUTH_DBTYPE
              value: mysql
            - name: SOURCE_EU_AUTH_SSL_CACERT
              value: /etc/ssl/certs/ca.pem
            - name: SOURCE_EU_AUTH_WRITE_GROUPS
              value: auth-writers
            - name: SOURCE_EU_AUTH_READ_GROUPS
              value: auth-readers
```

Образ, который публикует тег, — `ghcr.io/<owner>/<repo>:<tag>`. В примере подставьте тег из нужного релиза.
