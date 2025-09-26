DROP TRIGGER IF EXISTS trigger_update_workflows ON workflows;

DROP FUNCTION IF EXISTS update_workflows_updated_at;

DROP INDEX IF EXISTS idx_workflows_created_at_desc_id_desc;
DROP INDEX IF EXISTS idx_workflows_user_id;

DROP TABLE IF EXISTS workflows;

DROP TYPE IF EXISTS WORKFLOW_BUILD_STATUS;