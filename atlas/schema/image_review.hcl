table "ifk_image_predictions" {
  schema  = schema.default
  charset = "utf8mb4"
  collate = "utf8mb4_unicode_ci"
  column "id" {
    null           = false
    type           = bigint
    auto_increment = true
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
  column "url" {
    null = false
    type = text
  }
  column "url_sha256" {
    null = false
    type = varchar(64)
  }
  column "sha256" {
    null = false
    type = varchar(64)
  }
  column "model_sha256" {
    null = false
    type = varchar(64)
  }
  column "label" {
    null = false
    type = varchar(128)
  }
  column "correction" {
    null    = false
    type    = varchar(128)
    default = ""
  }
  column "reviewer" {
    null    = false
    type    = varchar(128)
    default = ""
  }
  column "confidence" {
    null    = false
    type    = double
    default = 0
  }
  column "revision" {
    null    = false
    type    = bigint
    default = 0
  }
  column "reviewed_at" {
    null    = false
    type    = bigint
    default = 0
  }
  primary_key {
    columns = [column.id]
  }
  index "idx_image_prediction_identity" {
    unique  = true
    columns = [column.url_sha256, column.sha256, column.model_sha256]
  }
  index "idx_image_prediction_confidence" {
    columns = [column.confidence]
  }
  index "idx_image_prediction_label_confidence" {
    columns = [column.label, column.confidence]
  }
  index "idx_image_prediction_review_confidence" {
    columns = [column.correction, column.confidence]
  }
}
