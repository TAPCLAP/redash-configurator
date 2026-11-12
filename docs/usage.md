# Using the configurator

[Русская версия](usage.ru.md)

The process reads the environment, reconciles Redash, sleeps for `SYNC_INTERVAL`, and repeats until SIGINT or SIGTERM. The first pass starts immediately. One broken source does not stop the others. Passwords are never written to the log.

The API key must belong to an admin user. Redash accepts it as `Authorization: Key <api-key>`. Creating and editing data sources, testing a connection, and managing groups are admin routes.

## Environment

| Variable | Required | Meaning |
|---|---|---|
| `REDASH_URL` | yes | Absolute `http` or `https` URL of Redash. A trailing slash is removed |
| `REDASH_API_KEY` | yes | Admin user API key |
| `SYNC_INTERVAL` | no | Go duration, default `5m`. `30s` and `1h` are valid. Zero and negative values are rejected |
| `SOURCE_<ID>_NAME` | yes | Data source name in Redash. Finding this key is how a source is discovered |
| `SOURCE_<ID>_HOST` | yes | MySQL host |
| `SOURCE_<ID>_PORT` | yes | TCP port from 1 to 65535 |
| `SOURCE_<ID>_USER` | yes | MySQL user |
| `SOURCE_<ID>_PASSWORD` | yes | MySQL password. Leading and trailing spaces are kept |
| `SOURCE_<ID>_DBNAME` | yes | Database name. Sent to Redash as `db` |
| `SOURCE_<ID>_DBTYPE` | yes | Only `mysql`. Case does not matter |
| `SOURCE_<ID>_SSL_CACERT` | no | CA file path on the Redash server. Empty by default |
| `SOURCE_<ID>_WRITE_GROUPS` | no | Comma-separated group names that receive full access |
| `SOURCE_<ID>_READ_GROUPS` | no | Comma-separated group names that receive view-only access |

`<ID>` matches `[A-Z0-9_]+`. `SOURCE_EU_AUTH_NAME` has id `EU_AUTH`. A key that ends in `_DBNAME` is the database name, not another source. Two ids with the same `NAME` are a startup error. So is a group listed in both write and read. Zero sources is a startup error. Names, hosts, users, database names, and CA paths are trimmed. Group names are trimmed, empty items are dropped, and repeats inside one list are ignored.

These options are always sent and are not read from the environment:

- `use_ssl`: `true`
- `ssl_mode`: `required`

Redash stores the password in the option `passwd`. An empty CA path is omitted. Other MySQL options already stored on the source, such as `charset`, are left in place when the source is updated.

Sources that disappear from the environment are left in Redash.

## What one pass does

1. List groups. Create every write and read group that is missing. New groups receive Redash's default permissions. Users are not added to groups.
2. Find each data source by the exact `NAME`.
3. Create it, or update it when host, port, user, database, SSL mode, CA path, or type differ. The password from the environment is sent together with those fields.
4. When those fields already match, call `POST /api/data_sources/<id>/test`.
5. Make group links match the lists.

Redash does not return the password or a hash. `GET /api/data_sources/<id>` replaces `passwd` with `--------`. That placeholder cannot be compared with the environment. The connection test is the check:

- `ok: true` means the stored password works. It is not rewritten.
- `ok: false` and a message containing `1045` or `Access denied` means MySQL rejected the password. The configurator writes the password from the environment and tests again.
- Any other failure, such as a refused connection or an unknown database, does not prove the password is wrong. The password is left as stored, and the message is logged.

A test also runs after every create and update. The HTTP client waits up to two minutes because Redash answers only after its test job finishes.

Creating a data source also links Redash's builtin `default` group with full access. Builtin groups (`default`, `admin`) that are not named in the lists are left untouched. A regular group that is no longer in either list is unlinked from the source. The group object itself is not deleted. A builtin group that is named in a list gets the access that list asks for.

Full access is `view_only: false`. View only is `view_only: true`. Redash adds a new link as full access, so a read group is added and then switched to view only.

If Redash already has two data sources with the same managed name, or two groups with the same required name, that source or that pass fails and is retried on the next interval.

## Deployment

Run a single replica. The container does not listen on a port. The CA file is not mounted here. Redash reads it from its own filesystem.

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
          image: ghcr.io/tapclap/redash-configurator:v0.0.1
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

The image published by a tag is `ghcr.io/<owner>/<repo>:<tag>`. Replace the example image with the tag from that release.
