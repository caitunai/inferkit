-- Create "ifk_image_predictions" table
CREATE TABLE `ifk_image_predictions` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `url` text NOT NULL,
  `url_sha256` varchar(64) NOT NULL,
  `sha256` varchar(64) NOT NULL,
  `model_sha256` varchar(64) NOT NULL,
  `label` varchar(128) NOT NULL,
  `prediction_json` text NOT NULL,
  `correction` varchar(128) NOT NULL DEFAULT "",
  `reviewer` varchar(128) NOT NULL DEFAULT "",
  `confidence` double NOT NULL DEFAULT 0,
  `revision` bigint NOT NULL DEFAULT 0,
  `reviewed_at` bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  INDEX `idx_image_prediction_confidence` (`confidence`),
  UNIQUE INDEX `idx_image_prediction_identity` (`url_sha256`, `sha256`, `model_sha256`),
  INDEX `idx_image_prediction_label_confidence` (`label`, `confidence`),
  INDEX `idx_image_prediction_review_confidence` (`correction`, `confidence`)
) CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
