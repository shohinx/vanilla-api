BEGIN;

CREATE UNIQUE INDEX IF NOT EXISTS menus_id_restaurant_unique
    ON menus (id, restaurant_id);

CREATE TABLE IF NOT EXISTS menu_schedules (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    restaurant_id uuid NOT NULL,
    menu_id uuid NOT NULL,
    weekday_mask smallint NOT NULL DEFAULT 127 CHECK (weekday_mask BETWEEN 1 AND 127),
    start_date date,
    end_date date,
    start_local_time time,
    end_local_time time,
    priority integer NOT NULL DEFAULT 0,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (menu_id, restaurant_id) REFERENCES menus(id, restaurant_id) ON DELETE RESTRICT,
    FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE RESTRICT,
    CHECK (end_date IS NULL OR start_date IS NULL OR end_date >= start_date),
    CHECK ((start_local_time IS NULL) = (end_local_time IS NULL)),
    CHECK (start_local_time IS NULL OR start_local_time <> end_local_time)
);

CREATE INDEX IF NOT EXISTS menu_schedules_menu_priority_idx
    ON menu_schedules (menu_id, status, priority DESC, id);
CREATE INDEX IF NOT EXISTS menu_schedules_restaurant_idx
    ON menu_schedules (restaurant_id, menu_id);

CREATE OR REPLACE FUNCTION touch_menu_schedule_updated_at()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = public, pg_catalog
AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS menu_schedules_touch_updated_at ON menu_schedules;
CREATE TRIGGER menu_schedules_touch_updated_at
BEFORE UPDATE ON menu_schedules
FOR EACH ROW EXECUTE FUNCTION touch_menu_schedule_updated_at();

INSERT INTO schema_migrations (version)
VALUES ('000002_menu_schedules_versions')
ON CONFLICT (version) DO NOTHING;

COMMIT;
