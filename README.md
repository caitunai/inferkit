# Go-Blueprint
The project template of Golang.

## Image Filter

- [第三方图片分类 API 接入文档](docs/IMAGE_FILTER_API.md)
- [识别结果采集、筛选列表与逐张人工纠错](docs/IMAGE_REVIEW.md)
- [Go CPU 部署、CLI 和浏览器检测工作台](docs/GO_ONNX_SERVER.md)
- [Debian 11 x86_64 原生 CPU 部署与 ONNX Runtime 兼容检查](docs/DEBIAN11_DEPLOYMENT.md)
- [Debian 13 amd64 Docker 镜像、GitHub Actions 与 GHCR / Docker Hub 部署](docs/DOCKER_DEPLOYMENT.md)
- [ONNX 模型部署协议](docs/ONNX_DEPLOYMENT.md)

## How to use
```shell
gonew github.com/caitunai/go-blueprint@v1.9.29 github.com/yourname/project
```

## Install gonew
```shell
go install golang.org/x/tools/cmd/gonew@latest
```

## ⚠️ Update the hardcode
> ⚠️ Replace every occurrence of `github.com/caitunai/go-blueprint` in
> [`.golangci.yaml`](https://github.com/caitunai/go-blueprint/blob/main/.golangci.yaml)
> with the new module path. This keeps both `depguard` and the Go formatters aligned with `go.mod`.

## Develop and Run
install air
```shell
go install github.com/air-verse/air@latest
```
create the configuration and update the content
```shell
cp .app.toml.example .app.toml
vim .app.toml
```
then run project created by this template
```shell
air serve
```

## Lint code and commit
lint the code
```shell
golangci-lint run
```

Apply the configured formatters and safe automatic fixes:

```shell
golangci-lint fmt
golangci-lint run --fix
```

The `fieldalignment` analyzer is enforced to reduce structure padding. Review field-order-sensitive
serialization, reflection, ORM, and tests before applying its fixes:

```shell
fieldalignment -fix ./path/to/package
```

Install the analyzer when it is not already available:

```shell
go install golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@latest
```

After code style fixed, you can commit the code
```shell
git add .
git commit -m "commit_message"
```

## Build to release
```shell
cd storage/ui
npm ci
npm run build
cd ../..
GOOS=linux GOARCH=amd64 go build
```

## Configuration Center

The built-in configuration center is disabled by default. Enable it and set
the mandatory Basic Auth credentials in `.app.toml` before using the management
page or any configuration-center API:

```toml
[configcenter]
enabled=true
username="config-admin"
password="replace-with-at-least-16-random-characters"
```

The username must be 1-128 characters, without surrounding whitespace or a
colon. The password must be 16-256 characters. Startup fails if the feature is
enabled with invalid credentials. When disabled, `/config-center` and all
routes below it return `404`. Basic Auth protects the management page and
management APIs; always expose them through HTTPS because Basic Auth encodes
credentials but does not encrypt them.

The management UI is available at `/config-center`. It supports visual editing
for objects, arrays, strings, booleans, and JSON numbers. Environments can inherit
from one parent environment; objects merge recursively, while arrays and scalar
values replace the inherited value.

Configuration edits are saved as drafts. Use the multi-environment publish
action to create immutable, fully resolved snapshots in one database
transaction. Apply the Atlas migrations before first use:

```shell
atlas migrate apply --env local
```

### Configuration encryption

Draft configurations, draft descriptions, published configurations, and
published descriptions support application-layer envelope encryption. The
external keyring must not be stored in this repository, in the configuration
center database, or in a configuration namespace. The application config only
contains the provider, active key ID, and absolute keyring path:

```toml
[configcrypt]
enabled=true
provider="file"
activeKey="config-key-v1"
keyring="/run/secrets/config-center/keys.json"
```

Generate the first AES-256 key without printing the key material to the
terminal. The command creates or updates the keyring with `0600` permissions:

```shell
go run . config-key generate --config .app.toml --id config-key-v1
```

After enabling encryption on a database that already contains configuration
data, encrypt legacy plaintext records:

```shell
go run . config-key reencrypt --config .app.toml
```

To rotate the key-encryption key, generate a new key ID in the same external
keyring, update `activeKey`, restart the service, and run `reencrypt` again:

```shell
go run . config-key generate --config .app.toml --id config-key-v2
# Update activeKey to config-key-v2 and restart the service.
go run . config-key reencrypt --config .app.toml
```

The re-encryption command rewraps stored data-encryption keys with the active
key and can be safely rerun. Keep old keys in the external keyring until all
records are migrated and backups encrypted under those keys have expired. A
lost key cannot be recovered from the database.

### Published configuration API

Applications can read the latest published, fully resolved configuration by
namespace and environment slug. Draft values are never returned by this API:

```text
GET /config-center/api/runtime/{namespace}/{environment}?format=json
```

Supported formats are `json` (default), `yaml`, `toml`, `env`, and `ini`.
JSON uses the normal API response envelope and exposes configuration under
`data.config`, configuration descriptions under `data.descriptions`, and the
release reason under `data.reason`. Text formats return the
configuration directly and include configuration descriptions as comments;
release metadata remains in the JSON response and response headers.

Every namespace has its own API Key. It is accepted only through the
`X-API-Key` request header, is never returned by the namespace API, and is
stored with the same external-keyring envelope encryption used by configuration
payloads. Creating a namespace requires a 32-256 character URL-safe API Key.
When editing a namespace, leave the field empty to keep the existing key or
enter a new value to rotate it. Existing namespaces deny runtime API access
until an API Key has been configured. The runtime API does not require the
management Basic Auth credentials, but it is available only while
`configcenter.enabled=true` and still requires the namespace `X-API-Key`.

Open the management API with Basic Auth, for example:

```shell
curl --user 'config-admin:replace-with-the-config-center-password' \
  'https://config.example.com/config-center/api/namespaces'
```

```shell
curl -H 'Accept: application/json' \
  -H 'X-API-Key: replace-with-the-namespace-api-key' \
  'http://127.0.0.1:8080/config-center/api/runtime/my-service/production'

curl -H 'X-API-Key: replace-with-the-namespace-api-key' \
  'http://127.0.0.1:8080/config-center/api/runtime/my-service/production?format=yaml'
```

Responses include `X-Config-Namespace`, `X-Config-Environment`,
`X-Config-Version`, `X-Config-Batch`, and `Last-Modified` metadata headers and
use `Cache-Control: no-store` because published configuration may contain
sensitive values.

### Loading published configuration into Viper

The application can merge the latest published configurations for an ordered
list of namespace/environment targets into Viper from the root command before
any child command runs. This makes the same configuration available to
`serve`, `queue`, migration, and other commands. Targets are loaded and merged
in declaration order, so a later target overrides matching values from earlier
targets while retaining unrelated values. This supports layers such as shared
defaults, service settings, and deployment-specific overrides.

`.app.toml` remains the bootstrap source, while every target independently
selects a trusted direct database read or an HTTP runtime API. Draft
configuration is never loaded. A single load can therefore combine local
database configuration with configuration from multiple independent servers.

The following example loads shared defaults from the local database and then
applies overrides from two different configuration-center servers:

```toml
[configload]
enabled=true

[[configload.targets]]
source="database"
namespace="shared"
environment="base"

[[configload.targets]]
source="http"
namespace="my-service"
environment="production"

[configload.targets.http]
baseURL="https://primary-config.example.com"
apiKeyEnv="PRIMARY_CONFIG_API_KEY"
timeout="5s"

[[configload.targets]]
source="http"
namespace="regional-service"
environment="production"

[configload.targets.http]
baseURL="https://regional-config.example.com"
apiKeyEnv="REGIONAL_CONFIG_API_KEY"
timeout="8s"
```

Set `PRIMARY_CONFIG_API_KEY` and `REGIONAL_CONFIG_API_KEY` in the process
environment before startup. `apiKeyEnv` names the environment variable; its
value is read into memory and is never included in errors or logs. This allows
targets using the same namespace on different servers to use different API
Keys without storing plaintext credentials in `.app.toml`.

Database targets use the local `[db]` connection and do not require a namespace
API Key. If a database snapshot is encrypted, the local `[configcrypt]` keyring
must contain the required key. `config-key generate` skips the complete target
list when it contains any database target, because applying only the HTTP
portion would violate atomic ordered loading while the command may still be
bootstrapping the first local keyring.

Every target must explicitly define `source`, `namespace`, and `environment`.
HTTP targets must additionally define their own `http.baseURL` and
`http.apiKeyEnv`. The per-target HTTP timeout defaults to five seconds when
omitted, and otherwise must be greater than zero and at most one minute.
Redirects are rejected so API Keys cannot be forwarded to another origin, and
every response body is limited to 2 MiB. Global source, HTTP, namespace, and
environment fallback settings are not supported.

Published values override ordinary `.app.toml` values, while Viper environment
variables retain their higher precedence. The bootstrap roots `db`,
`configcrypt`, `configcenter`, and `configload` are never accepted from the
published payload, preventing a remote configuration from changing its own
loader, database connection, keyring, or configuration-center authentication.
Redis, queue, logger, server, and other application settings are initialized
after all published configurations are merged. Every target is fetched before
Viper is changed; a missing, invalid, duplicate, or failed target stops startup
without applying only part of the configured target list.

For Vite hot reload, set `ui.assetMode="vite"`, run `npm run dev` in
`storage/ui`, and keep `ui.viteDevOrigin` aligned with the Vite origin. Release
builds must run the frontend build before compiling the Go binary because the
generated manifest and assets are embedded by `storage/view.go`.

Absolutely! Here’s the **updated README tutorial** including Atlas installation instructions:

---

# Database Migration with Atlas

This guide explains how to install Atlas, set up a baseline for an existing MySQL database, and manage schema migrations for multiple environments (`prod`, `dev`, `local`).

---

## 1. Install Atlas

Atlas provides prebuilt binaries for different platforms. Follow the steps below to install it:

### **macOS (Homebrew)**

```bash
brew install ariga/tap/atlas
```

### **Linux (using shell script)**

```bash
curl -sSf https://atlasgo.sh | sh
```

* This downloads and installs the latest Atlas binary in your system.

### **Verify Installation**

```bash
atlas version
```

You should see the installed Atlas version printed.

---
## 2. Configure Environment Variables (`.env`)

Atlas uses environment variables to store sensitive database credentials. Create a `.env` file in your project root:

```env
DB_DEV_USERNAME="dev"
DB_DEV_PASSWORD="devpassword"
DB_DEV_HOST="localhost"
DB_DEV_PORT="3306"
DB_DEV_NAME="devdb"

DB_LOCAL_USERNAME="localuser"
DB_LOCAL_PASSWORD="localpassword"
DB_LOCAL_HOST="localhost"
DB_LOCAL_PORT="3306"
DB_LOCAL_NAME="localdb"

DB_PROD_USERNAME="produser"
DB_PROD_PASSWORD="prodpassword"
DB_PROD_HOST="localhost"
DB_PROD_PORT="3306"
DB_PROD_NAME="productiondb"
```

### Automatically load `.env` for Atlas

The repository provides `scripts/atlas-env.sh`, a portable wrapper for Zsh,
Bash, and POSIX `sh`. The script is project-independent: it searches upward
from the current directory for the nearest `atlas.hcl`, loads the `.env` beside
it in an isolated subshell, and then calls the installed Atlas executable with
the original arguments. The loaded values do not remain in the interactive
shell after Atlas exits.

Install one user-level copy from the repository root. The command respects
`XDG_CONFIG_HOME` and otherwise installs to `~/.config/atlas/`. The directory
is private to the user (`0700`), and the sourced script is readable and
writable only by the user (`0600`):

```sh
install -d -m 0700 "${XDG_CONFIG_HOME:-$HOME/.config}/atlas"
install -m 0600 scripts/atlas-env.sh \
  "${XDG_CONFIG_HOME:-$HOME/.config}/atlas/atlas-env.sh"
```

The installed file intentionally has no executable bit because shell startup
files load it with `source` or the POSIX dot command.

Do not reference the script inside each project from a shell startup file.
Load the single user-level copy once for each shell you use. After it is
loaded, the command name remains `atlas` in every Atlas project.

For **Zsh**, copy this line into `~/.zshrc`:

```zsh
source "${XDG_CONFIG_HOME:-$HOME/.config}/atlas/atlas-env.sh"
```

Then reload the configuration:

```zsh
source ~/.zshrc
```

For **Bash**, copy the same line into `~/.bashrc`, or into `~/.bash_aliases` if
that file is already loaded by `~/.bashrc`:

```bash
source "${XDG_CONFIG_HOME:-$HOME/.config}/atlas/atlas-env.sh"
```

Then reload the selected file, for example:

```bash
source ~/.bashrc
```

For a POSIX-compatible **sh**, use the dot command in `~/.profile` or the
startup file referenced by the shell's `ENV` variable:

```sh
. "${XDG_CONFIG_HOME:-$HOME/.config}/atlas/atlas-env.sh"
```

Start a new shell or reload its startup file, then verify that `atlas` resolves
to a function and still invokes the installed executable:

```bash
type atlas
atlas version
```

All Atlas subcommands keep their original syntax:

```bash
atlas migrate diff add_image_predictions --env local
atlas migrate apply --env local
atlas schema inspect --env local
```

The wrapper uses the shell's `source`/dot behavior, so `.env` must be a trusted
developer-maintained file. Do not copy an untrusted or remotely supplied file
to the project `.env`. If no matching `atlas.hcl` or `.env` is found, the
wrapper calls the installed Atlas executable without loading a file.

To update the user-level wrapper after pulling a newer version of this
repository, install the file again. This also restores the intended `0600`
permission if it changed:

```sh
install -m 0600 scripts/atlas-env.sh \
  "${XDG_CONFIG_HOME:-$HOME/.config}/atlas/atlas-env.sh"
```

### Synchronize the Atlas table prefix before generating migrations

The GORM table prefix is configured separately from the Atlas schema:

```toml
[db.table]
prefix="ifk"
```

Changing `db.table.prefix` does not automatically rename the tables declared
in Atlas. Before running `atlas migrate diff`, update every table definition in
all active files in `atlas/schema/` (`config_center.hcl` and `image_review.hcl`)
to use the new prefix. GORM adds an underscore when a non-empty prefix does not
already end in one, so `prefix="ifk"` corresponds to Atlas table names such as
`ifk_image_predictions` and `ifk_config_namespaces`.

Also update HCL references that contain the table label, such as
`table.ifk_config_namespaces.column.id`. Do not generate a migration until all
active schema files, their references, and the configured prefix agree. If the
generated SQL unexpectedly drops and recreates many tables, stop and check the
prefixes before validating or applying the migration.

---

## 3. Generate the Baseline

Inspect your production database and create a baseline schema snapshot:

```bash
atlas schema inspect --env prod > atlas/schema/0-baseline.hcl
```

* Generates `0-baseline.hcl`, capturing the current database structure.
* Baseline represents the starting point for Atlas migration management.

---

## 4. Generate the Initial Migration

Generate an initial migration based on the baseline:

```bash
atlas migrate diff initial --env prod
```

* Creates migration SQL in the `atlas/migrations` directory.
* SQL is generated but not yet applied.

---

## 5. Apply the Baseline

Tell Atlas that the current production database corresponds to the baseline version:

```bash
atlas migrate apply --env prod --baseline 20251120060720
```

* Replace `20251120060720` with the version number from your baseline migration file.
* Atlas records the baseline in the `atlas_schema_migrations` table.
* **No SQL will be executed**, as the database already matches the baseline.

---

## 6. Create and Apply New Migrations

After baseline setup, create new schema migrations for changes to the active schema:

```bash
atlas migrate diff update_image_review --env prod
```

If the migration must insert default, lookup, or other initialization data,
edit the newly generated SQL file in `atlas/migrations` before applying it.
Place data statements in dependency-safe order. For example, insert a default
row after creating its table and before backfilling existing rows or enforcing
a foreign key that references it.

After manually editing a migration file, recalculate the directory checksum,
validate the migration directory, and then apply it:

```bash
atlas migrate hash --env prod
atlas migrate validate --env prod
atlas migrate apply --env prod
```

* `migrate diff` generates the structural migration SQL.
* Data SQL such as `INSERT` statements is added manually when required.
* `migrate hash` updates `atlas/migrations/atlas.sum` after manual edits.
* `migrate validate` checks the complete migration directory before application.
* `migrate apply` executes the migration and updates the migration history.

---

## 7. Switch to Other Environments

Once the baseline is established in `prod`, you can repeat migration steps for `dev` or `local`:

1. Configure environment variables in `atlas.hcl` for each environment.
2. Generate the structural migration:

```bash
atlas migrate diff <migration_name> --env local
```

3. If initialization data is required, edit the generated SQL file and add the
   necessary data statements before applying the migration.
4. Recalculate the checksum after editing the migration file:

```bash
atlas migrate hash --env local
```

5. Validate the migration directory:

```bash
atlas migrate validate --env local
```

6. Apply the migration:

```bash
atlas migrate apply --env local
```

* Keeps all environments synchronized with the same migration history.

---

### Notes

* Always ensure the baseline file accurately reflects the current database schema.
* Baseline versions are **environment-specific**; each environment must initialize its baseline independently.
* Atlas migration only tracks schema changes; it does **not restore deleted data**. Always back up your database before applying destructive migrations.

---


**Thanks**
