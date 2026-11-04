table "ifk_config_namespaces" {
  schema = schema.default
  charset = "utf8mb4"
  collate = "utf8mb4_unicode_ci"
  column "id" {
    null           = false
    type           = bigint
    auto_increment = true
  }
  column "name" {
    null = false
    type = varchar(100)
  }
  column "slug" {
    null = false
    type = varchar(64)
  }
  column "description" {
    null    = false
    type    = varchar(500)
    default = ""
  }
  column "api_key" {
    null = false
    type = mediumtext
  }
  column "created_at" {
    null    = false
    type    = timestamp
    default = sql("CURRENT_TIMESTAMP")
  }
  column "updated_at" {
    null    = false
    type    = timestamp
    default = sql("CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP")
  }
  primary_key {
    columns = [column.id]
  }
  index "idx_ifk_config_namespaces_slug" {
    unique  = true
    columns = [column.slug]
  }
}

table "ifk_config_environments" {
  schema = schema.default
  charset = "utf8mb4"
  collate = "utf8mb4_unicode_ci"
  column "id" {
    null           = false
    type           = bigint
    auto_increment = true
  }
  column "name" {
    null = false
    type = varchar(100)
  }
  column "namespace_id" {
    null = false
    type = bigint
  }
  column "slug" {
    null = false
    type = varchar(64)
  }
  column "description" {
    null    = false
    type    = varchar(500)
    default = ""
  }
  column "parent_id" {
    null    = false
    type    = bigint
    default = 0
  }
  column "draft_config" {
    null = false
    type = mediumtext
  }
  column "draft_descriptions" {
    null = false
    type = mediumtext
  }
  column "created_at" {
    null    = false
    type    = timestamp
    default = sql("CURRENT_TIMESTAMP")
  }
  column "updated_at" {
    null    = false
    type    = timestamp
    default = sql("CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP")
  }
  primary_key {
    columns = [column.id]
  }
  index "idx_config_environment_namespace_slug" {
    unique  = true
    columns = [column.namespace_id, column.slug]
  }
  index "idx_ifk_config_environments_parent_id" {
    columns = [column.parent_id]
  }
  foreign_key "fk_ifk_config_environments_namespace" {
    columns     = [column.namespace_id]
    ref_columns = [table.ifk_config_namespaces.column.id]
    on_update   = NO_ACTION
    on_delete   = CASCADE
  }
}

table "ifk_config_releases" {
  schema = schema.default
  charset = "utf8mb4"
  collate = "utf8mb4_unicode_ci"
  column "id" {
    null           = false
    type           = bigint
    auto_increment = true
  }
  column "environment_id" {
    null = false
    type = bigint
  }
  column "batch_id" {
    null = false
    type = varchar(32)
  }
  column "version" {
    null = false
    type = bigint
  }
  column "config" {
    null = false
    type = mediumtext
  }
  column "descriptions" {
    null = false
    type = mediumtext
  }
  column "reason" {
    null    = false
    type    = varchar(1000)
    default = ""
  }
  column "created_at" {
    null    = false
    type    = timestamp
    default = sql("CURRENT_TIMESTAMP")
  }
  column "updated_at" {
    null    = false
    type    = timestamp
    default = sql("CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP")
  }
  primary_key {
    columns = [column.id]
  }
  index "idx_ifk_config_releases_batch_id" {
    columns = [column.batch_id]
  }
  index "idx_config_release_version" {
    unique  = true
    columns = [column.environment_id, column.version]
  }
  foreign_key "fk_ifk_config_releases_environment" {
    columns     = [column.environment_id]
    ref_columns = [table.ifk_config_environments.column.id]
    on_update   = NO_ACTION
    on_delete   = CASCADE
  }
}
