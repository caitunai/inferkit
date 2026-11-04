-- Create "ifk_config_namespaces" table
CREATE TABLE `ifk_config_namespaces` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `name` varchar(100) NOT NULL,
  `slug` varchar(64) NOT NULL,
  `description` varchar(500) NOT NULL DEFAULT "",
  `api_key` mediumtext NOT NULL,
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE INDEX `idx_ifk_config_namespaces_slug` (`slug`)
) CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
-- Create "ifk_config_environments" table
CREATE TABLE `ifk_config_environments` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `name` varchar(100) NOT NULL,
  `namespace_id` bigint NOT NULL,
  `slug` varchar(64) NOT NULL,
  `description` varchar(500) NOT NULL DEFAULT "",
  `parent_id` bigint NOT NULL DEFAULT 0,
  `draft_config` mediumtext NOT NULL,
  `draft_descriptions` mediumtext NOT NULL,
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE INDEX `idx_config_environment_namespace_slug` (`namespace_id`, `slug`),
  INDEX `idx_ifk_config_environments_parent_id` (`parent_id`),
  CONSTRAINT `fk_ifk_config_environments_namespace` FOREIGN KEY (`namespace_id`) REFERENCES `ifk_config_namespaces` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
) CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
-- Create "ifk_config_releases" table
CREATE TABLE `ifk_config_releases` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `environment_id` bigint NOT NULL,
  `batch_id` varchar(32) NOT NULL,
  `version` bigint NOT NULL,
  `config` mediumtext NOT NULL,
  `descriptions` mediumtext NOT NULL,
  `reason` varchar(1000) NOT NULL DEFAULT "",
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE INDEX `idx_config_release_version` (`environment_id`, `version`),
  INDEX `idx_ifk_config_releases_batch_id` (`batch_id`),
  CONSTRAINT `fk_ifk_config_releases_environment` FOREIGN KEY (`environment_id`) REFERENCES `ifk_config_environments` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
) CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
