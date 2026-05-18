-- Create "monitors" table
CREATE TABLE `monitors` (
  `id` text NULL,
  `name` text NOT NULL,
  `type` text NOT NULL,
  `enabled` integer NOT NULL DEFAULT 1,
  `interval_seconds` integer NOT NULL,
  `timeout_seconds` integer NOT NULL,
  `config_json` text NOT NULL,
  `notifications_enabled` integer NOT NULL DEFAULT 1,
  `created_at` text NOT NULL,
  `updated_at` text NOT NULL,
  `deleted_at` text NULL,
  PRIMARY KEY (`id`)
);
-- Create "monitor_states" table
CREATE TABLE `monitor_states` (
  `monitor_id` text NULL,
  `state` text NOT NULL,
  `last_check_id` text NULL,
  `last_checked_at` text NULL,
  `last_success_at` text NULL,
  `last_failure_at` text NULL,
  `consecutive_successes` integer NOT NULL DEFAULT 0,
  `consecutive_failures` integer NOT NULL DEFAULT 0,
  `updated_at` text NOT NULL,
  PRIMARY KEY (`monitor_id`),
  CONSTRAINT `0` FOREIGN KEY (`monitor_id`) REFERENCES `monitors` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create "check_results" table
CREATE TABLE `check_results` (
  `id` text NULL,
  `monitor_id` text NOT NULL,
  `started_at` text NOT NULL,
  `finished_at` text NOT NULL,
  `duration_ms` integer NOT NULL,
  `success` integer NOT NULL,
  `state` text NOT NULL,
  `error` text NULL,
  `http_status_code` integer NULL,
  PRIMARY KEY (`id`),
  CONSTRAINT `0` FOREIGN KEY (`monitor_id`) REFERENCES `monitors` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "idx_check_results_monitor_started" to table: "check_results"
CREATE INDEX `idx_check_results_monitor_started` ON `check_results` (`monitor_id`, `started_at` DESC);
-- Create "incidents" table
CREATE TABLE `incidents` (
  `id` text NULL,
  `monitor_id` text NOT NULL,
  `started_at` text NOT NULL,
  `resolved_at` text NULL,
  `start_event_id` text NULL,
  `end_event_id` text NULL,
  `reason` text NULL,
  PRIMARY KEY (`id`),
  CONSTRAINT `0` FOREIGN KEY (`monitor_id`) REFERENCES `monitors` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "idx_incidents_monitor_started" to table: "incidents"
CREATE INDEX `idx_incidents_monitor_started` ON `incidents` (`monitor_id`, `started_at` DESC);
-- Create "events" table
CREATE TABLE `events` (
  `id` text NULL,
  `type` text NOT NULL,
  `monitor_id` text NULL,
  `data_json` text NOT NULL DEFAULT '{}',
  `created_at` text NOT NULL,
  PRIMARY KEY (`id`),
  CONSTRAINT `0` FOREIGN KEY (`monitor_id`) REFERENCES `monitors` (`id`) ON UPDATE NO ACTION ON DELETE SET NULL
);
-- Create index "idx_events_created" to table: "events"
CREATE INDEX `idx_events_created` ON `events` (`created_at` DESC);
-- Create index "idx_events_monitor_created" to table: "events"
CREATE INDEX `idx_events_monitor_created` ON `events` (`monitor_id`, `created_at` DESC);
-- Create "notification_targets" table
CREATE TABLE `notification_targets` (
  `id` text NULL,
  `name` text NOT NULL,
  `kind` text NOT NULL,
  `enabled` integer NOT NULL DEFAULT 1,
  `config_json` text NOT NULL,
  `created_at` text NOT NULL,
  `updated_at` text NOT NULL,
  `deleted_at` text NULL,
  PRIMARY KEY (`id`)
);
-- Create "notification_attempts" table
CREATE TABLE `notification_attempts` (
  `id` text NULL,
  `target_id` text NOT NULL,
  `monitor_id` text NULL,
  `incident_id` text NULL,
  `event_id` text NULL,
  `event_type` text NOT NULL,
  `status` text NOT NULL,
  `attempt_number` integer NOT NULL,
  `error` text NULL,
  `created_at` text NOT NULL,
  `sent_at` text NULL,
  PRIMARY KEY (`id`),
  CONSTRAINT `0` FOREIGN KEY (`event_id`) REFERENCES `events` (`id`) ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT `1` FOREIGN KEY (`incident_id`) REFERENCES `incidents` (`id`) ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT `2` FOREIGN KEY (`monitor_id`) REFERENCES `monitors` (`id`) ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT `3` FOREIGN KEY (`target_id`) REFERENCES `notification_targets` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- Create index "idx_notification_attempts_target_created" to table: "notification_attempts"
CREATE INDEX `idx_notification_attempts_target_created` ON `notification_attempts` (`target_id`, `created_at` DESC);
-- Create "settings" table
CREATE TABLE `settings` (
  `key` text NULL,
  `value_json` text NOT NULL,
  `updated_at` text NOT NULL,
  PRIMARY KEY (`key`)
);
