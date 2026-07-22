BEGIN;

-- Menus are edited directly; there is no draft, publish, or history layer.
DROP TABLE IF EXISTS menu_versions;
DROP FUNCTION IF EXISTS touch_menu_version_updated_at();

UPDATE menus SET status = 'inactive' WHERE status = 'draft';
ALTER TABLE menus DROP CONSTRAINT IF EXISTS menus_status_check;
ALTER TABLE menus
    ADD CONSTRAINT menus_status_check CHECK (status IN ('active', 'inactive'));

UPDATE menu_schedules SET status = 'inactive' WHERE status = 'archived';
ALTER TABLE menu_schedules DROP CONSTRAINT IF EXISTS menu_schedules_status_check;
ALTER TABLE menu_schedules
    ADD CONSTRAINT menu_schedules_status_check CHECK (status IN ('active', 'inactive'));

INSERT INTO schema_migrations (version)
VALUES ('000004_remove_menu_versions')
ON CONFLICT (version) DO NOTHING;

COMMIT;
