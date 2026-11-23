-- modify "ifk_image_predictions" table
ALTER TABLE `ifk_image_predictions` ADD INDEX `idx_image_prediction_correction_url` (`correction`, `url_sha256`);
