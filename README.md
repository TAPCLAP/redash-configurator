# redash-configurator

[Russian](README_ru.md)

The configurator keeps MySQL data sources in Redash aligned with environment variables. On a schedule it creates missing sources, updates connection settings, checks that the saved password still works, and grants groups full access or view-only access.

It talks to the Redash HTTP API as an admin user. It does not install Redash and it does not open a MySQL connection of its own. The CA path in `SOURCE_<ID>_SSL_CACERT` is a path on the Redash server.

## Example

`EU_AUTH` is only the name of the variable group. The data source in Redash is named by `SOURCE_EU_AUTH_NAME`.

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

Every pass sets MySQL `use_ssl` to true and `ssl_mode` to `required`. `auth-writers` can query and edit. `auth-readers` can view. Groups that do not exist yet are created. A second source is another `SOURCE_<ID>_NAME` group. There is no separate list of sources.

Field-by-field behavior, the password check, and a Deployment example are in [Using the configurator](docs/usage.md).

## Run

A published tag `v*` builds `linux/amd64` and `linux/arm64` and pushes `ghcr.io/<owner>/<repo>:<tag>`. Run one replica. Two replicas would repeat the same API calls.

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

Invalid configuration exits immediately with a non-zero status. A later API or connection error is logged, and the process keeps running until the next interval.

## Documentation

- [Using the configurator](docs/usage.md)

## Development

```bash
make test
make lint
make docker-build IMG=redash-configurator:latest
```

Go 1.25. `make help` lists the other targets.
