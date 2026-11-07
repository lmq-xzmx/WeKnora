-- Rollback of 000115_kb_maintenance. The table is pure audit/observability
-- state, so dropping it loses run history but no business data.
DROP TABLE IF EXISTS kb_maintenance_runs;
