BEGIN;

CREATE UNIQUE INDEX IF NOT EXISTS restaurants_id_organization_unique
    ON restaurants (id, organization_id);
CREATE UNIQUE INDEX IF NOT EXISTS allergens_id_organization_unique
    ON allergens (id, organization_id);
CREATE UNIQUE INDEX IF NOT EXISTS catalog_items_id_restaurant_unique
    ON catalog_items (id, restaurant_id);

CREATE TABLE IF NOT EXISTS catalog_item_allergens (
    catalog_item_id uuid NOT NULL,
    restaurant_id uuid NOT NULL,
    organization_id uuid NOT NULL,
    allergen_id uuid NOT NULL,
    relationship text NOT NULL CHECK (relationship IN ('contains', 'may_contain')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (catalog_item_id, allergen_id),
    FOREIGN KEY (catalog_item_id, restaurant_id)
        REFERENCES catalog_items(id, restaurant_id) ON DELETE CASCADE,
    FOREIGN KEY (restaurant_id, organization_id)
        REFERENCES restaurants(id, organization_id) ON DELETE CASCADE,
    FOREIGN KEY (allergen_id, organization_id)
        REFERENCES allergens(id, organization_id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS catalog_item_allergens_allergen_idx
    ON catalog_item_allergens (allergen_id, organization_id);
CREATE INDEX IF NOT EXISTS catalog_item_allergens_restaurant_idx
    ON catalog_item_allergens (restaurant_id, catalog_item_id);

DROP TRIGGER IF EXISTS catalog_item_allergens_refresh_public_menu_snapshot
    ON catalog_item_allergens;
CREATE TRIGGER catalog_item_allergens_refresh_public_menu_snapshot
AFTER INSERT OR UPDATE OR DELETE ON catalog_item_allergens
FOR EACH ROW EXECUTE FUNCTION refresh_public_menu_snapshot_from_child();

CREATE OR REPLACE FUNCTION refresh_public_menu_snapshot_from_allergen()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = public, pg_catalog
AS $$
DECLARE
    current_allergen_id uuid;
    current_restaurant_id uuid;
BEGIN
    current_allergen_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.id ELSE NEW.id END;

    FOR current_restaurant_id IN
        SELECT DISTINCT assignment.restaurant_id
        FROM catalog_item_allergens assignment
        WHERE assignment.allergen_id = current_allergen_id
    LOOP
        PERFORM refresh_public_menu_snapshot(current_restaurant_id);
    END LOOP;

    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

DROP TRIGGER IF EXISTS allergens_refresh_public_menu_snapshot ON allergens;
CREATE TRIGGER allergens_refresh_public_menu_snapshot
AFTER UPDATE OF name, code, description ON allergens
FOR EACH ROW EXECUTE FUNCTION refresh_public_menu_snapshot_from_allergen();

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
                                        'variants', item.variants,
                                        'allergens', COALESCE((
                                            SELECT jsonb_agg(
                                                jsonb_build_object(
                                                    'allergen_id', allergen.id,
                                                    'name', allergen.name,
                                                    'code', allergen.code,
                                                    'description', allergen.description,
                                                    'relationship', assignment.relationship
                                                )
                                                ORDER BY allergen.name, allergen.id
                                            )
                                            FROM catalog_item_allergens assignment
                                            JOIN allergens allergen
                                              ON allergen.id = assignment.allergen_id
                                             AND allergen.organization_id = assignment.organization_id
                                            WHERE assignment.catalog_item_id = item.id
                                        ), '[]'::jsonb)
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
VALUES ('000006_catalog_item_allergens')
ON CONFLICT (version) DO NOTHING;

COMMIT;
