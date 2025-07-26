DROP TRIGGER IF EXISTS trigger_update_analytics ON analytics;

DROP FUNCTION IF EXISTS update_analytics_updated_at;

DROP INDEX IF EXISTS idx_analytics_user;
DROP INDEX IF EXISTS idx_analytics_user_workflow;

DROP TABLE IF EXISTS analytics;