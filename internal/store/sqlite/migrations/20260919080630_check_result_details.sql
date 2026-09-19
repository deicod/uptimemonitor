-- Add column "details" to table: "check_results"
ALTER TABLE `check_results` ADD COLUMN `details` text NULL;
-- Backfill v0.1.0 HTTP status codes into the typed details payload (SPEC §13.4)
UPDATE `check_results` SET `details` = json_object('status_code', `http_status_code`) WHERE `http_status_code` IS NOT NULL;
-- Drop column "http_status_code" from table: "check_results"
ALTER TABLE `check_results` DROP COLUMN `http_status_code`;
