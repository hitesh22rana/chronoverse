ALTER TABLE workflows ADD COLUMN "log_retention" BOOLEAN NOT NULL DEFAULT TRUE;
UPDATE workflows SET log_retention = FALSE WHERE kind <> 'CONTAINER';