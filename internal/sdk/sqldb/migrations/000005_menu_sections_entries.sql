BEGIN;

CREATE UNIQUE INDEX IF NOT EXISTS catalog_items_id_restaurant_unique
    ON catalog_items (id, restaurant_id);

CREATE TABLE IF NOT EXISTS menu_sections (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    restaurant_id uuid NOT NULL,
    menu_id uuid NOT NULL,
    name text NOT NULL CHECK (btrim(name) <> ''),
    description text NOT NULL DEFAULT '',
    sort_order integer NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id, restaurant_id),
    FOREIGN KEY (menu_id, restaurant_id)
        REFERENCES menus(id, restaurant_id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS menu_sections_menu_name_unique
    ON menu_sections (menu_id, lower(name));
CREATE INDEX IF NOT EXISTS menu_sections_menu_order_idx
    ON menu_sections (menu_id, status, sort_order, id);

CREATE TABLE IF NOT EXISTS menu_entries (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    restaurant_id uuid NOT NULL,
    menu_section_id uuid NOT NULL,
    catalog_item_id uuid NOT NULL,
    sort_order integer NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (menu_section_id, catalog_item_id),
    FOREIGN KEY (menu_section_id, restaurant_id)
        REFERENCES menu_sections(id, restaurant_id) ON DELETE CASCADE,
    FOREIGN KEY (catalog_item_id, restaurant_id)
        REFERENCES catalog_items(id, restaurant_id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS menu_entries_section_order_idx
    ON menu_entries (menu_section_id, status, sort_order, id);
CREATE INDEX IF NOT EXISTS menu_entries_catalog_item_idx
    ON menu_entries (catalog_item_id, restaurant_id);

CREATE OR REPLACE FUNCTION touch_menu_section_updated_at()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = public, pg_catalog
AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION touch_menu_entry_updated_at()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = public, pg_catalog
AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS menu_sections_touch_updated_at ON menu_sections;
CREATE TRIGGER menu_sections_touch_updated_at
BEFORE UPDATE ON menu_sections
FOR EACH ROW EXECUTE FUNCTION touch_menu_section_updated_at();

DROP TRIGGER IF EXISTS menu_entries_touch_updated_at ON menu_entries;
CREATE TRIGGER menu_entries_touch_updated_at
BEFORE UPDATE ON menu_entries
FOR EACH ROW EXECUTE FUNCTION touch_menu_entry_updated_at();

DROP TRIGGER IF EXISTS menu_sections_refresh_public_menu_snapshot ON menu_sections;
CREATE TRIGGER menu_sections_refresh_public_menu_snapshot
AFTER INSERT OR UPDATE OR DELETE ON menu_sections
FOR EACH ROW EXECUTE FUNCTION refresh_public_menu_snapshot_from_child();

DROP TRIGGER IF EXISTS menu_entries_refresh_public_menu_snapshot ON menu_entries;
CREATE TRIGGER menu_entries_refresh_public_menu_snapshot
AFTER INSERT OR UPDATE OR DELETE ON menu_entries
FOR EACH ROW EXECUTE FUNCTION refresh_public_menu_snapshot_from_child();

CREATE OR REPLACE FUNCTION refresh_public_menu_snapshot(p_restaurant_id uuid)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public, pg_catalog
AS $$
DECLARE
    payload jsonb;
    menus_payload jsonb;
BEGIN
    payload := build_public_menu_payload(p_restaurant_id);
    IF payload IS NULL THEN
        DELETE FROM public_menu_snapshots WHERE restaurant_id = p_restaurant_id;
        RETURN;
    END IF;

    SELECT COALESCE(jsonb_agg(
        jsonb_build_object(
            'id', menu.id,
            'name', menu.name,
            'description', menu.description,
            'code', menu.code,
            'sections', COALESCE((
                SELECT jsonb_agg(
                    jsonb_build_object(
                        'id', section.id,
                        'name', section.name,
                        'description', section.description,
                        'entries', COALESCE((
                            SELECT jsonb_agg(
                                jsonb_build_object(
                                    'id', entry.id,
                                    'catalog_item', jsonb_build_object(
                                        'id', item.id,
                                        'name', item.name,
                                        'description', item.description,
                                        'price_cents', item.price_cents,
                                        'variants', item.variants
                                    )
                                ) ORDER BY entry.sort_order, item.name, entry.id
                            )
                            FROM menu_entries entry
                            JOIN catalog_items item
                              ON item.id = entry.catalog_item_id
                             AND item.restaurant_id = entry.restaurant_id
                            WHERE entry.menu_section_id = section.id
                              AND entry.restaurant_id = p_restaurant_id
                              AND entry.status = 'active'
                              AND item.status = 'active'
                        ), '[]'::jsonb)
                    ) ORDER BY section.sort_order, section.name, section.id
                )
                FROM menu_sections section
                WHERE section.menu_id = menu.id
                  AND section.restaurant_id = p_restaurant_id
                  AND section.status = 'active'
            ), '[]'::jsonb)
        ) ORDER BY menu.name, menu.id
    ), '[]'::jsonb)
    INTO menus_payload
    FROM menus menu
    WHERE menu.restaurant_id = p_restaurant_id
      AND menu.status = 'active';

    payload := jsonb_set(payload - 'catalog_items', '{menus}', menus_payload, true);

    INSERT INTO public_menu_snapshots (restaurant_id, payload_jsonb, etag, generated_at)
    VALUES (
        p_restaurant_id,
        payload,
        encode(digest(payload::text, 'sha256'), 'hex'),
        now()
    )
    ON CONFLICT (restaurant_id) DO UPDATE
    SET payload_jsonb = EXCLUDED.payload_jsonb,
        etag = EXCLUDED.etag,
        generated_at = EXCLUDED.generated_at;
END;
$$;

SELECT refresh_public_menu_snapshot(restaurant.id)
FROM restaurants restaurant;

INSERT INTO schema_migrations (version)
VALUES ('000005_menu_sections_entries')
ON CONFLICT (version) DO NOTHING;

COMMIT;
