BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS schema_migrations (
    version text PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
);

-- Only the current customer-facing document is retained.
CREATE TABLE IF NOT EXISTS public_menu_snapshots (
    restaurant_id uuid PRIMARY KEY REFERENCES restaurants(id) ON DELETE CASCADE,
    payload_jsonb jsonb NOT NULL,
    etag text NOT NULL CHECK (etag ~ '^[0-9a-f]{64}$'),
    generated_at timestamptz NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION build_public_menu_payload(p_restaurant_id uuid)
RETURNS jsonb
LANGUAGE sql
STABLE
SET search_path = public, pg_catalog
AS $$
    SELECT jsonb_build_object(
        'restaurant', jsonb_build_object(
            'id', restaurant.id,
            'name', restaurant.name,
            'slug', restaurant.slug,
            'timezone', restaurant.timezone,
            'currency', restaurant.currency,
            'business_hours', COALESCE((
                SELECT jsonb_agg(
                    jsonb_build_object(
                        'id', business_hour.id,
                        'day_of_week', business_hour.day_of_week,
                        'opens_at', business_hour.opens_at,
                        'closes_at', business_hour.closes_at
                    ) ORDER BY business_hour.day_of_week, business_hour.opens_at, business_hour.id
                )
                FROM business_hours business_hour
                WHERE business_hour.restaurant_id = restaurant.id
            ), '[]'::jsonb),
            'special_hours', COALESCE((
                SELECT jsonb_agg(
                    jsonb_build_object(
                        'id', special_hour.id,
                        'date', special_hour.special_date,
                        'opens_at', special_hour.opens_at,
                        'closes_at', special_hour.closes_at,
                        'is_closed', special_hour.is_closed,
                        'note', special_hour.note
                    ) ORDER BY special_hour.special_date, special_hour.id
                )
                FROM special_hours special_hour
                WHERE special_hour.restaurant_id = restaurant.id
            ), '[]'::jsonb)
        ),
        'menus', COALESCE((
            SELECT jsonb_agg(
                jsonb_build_object(
                    'id', menu.id,
                    'name', menu.name,
                    'description', menu.description,
                    'code', menu.code
                ) ORDER BY menu.name, menu.id
            )
            FROM menus menu
            WHERE menu.restaurant_id = restaurant.id
              AND menu.status = 'active'
        ), '[]'::jsonb),
        'catalog_items', COALESCE((
            SELECT jsonb_agg(
                jsonb_build_object(
                    'id', item.id,
                    'name', item.name,
                    'description', item.description
                ) ORDER BY item.name, item.id
            )
            FROM catalog_items item
            WHERE item.restaurant_id = restaurant.id
              AND item.status = 'active'
        ), '[]'::jsonb),
        'daily_specials', COALESCE((
            SELECT jsonb_agg(
                jsonb_build_object(
                    'id', special.id,
                    'name', special.name,
                    'description', special.description,
                    'starts_on', special.starts_on,
                    'ends_on', special.ends_on
                ) ORDER BY special.starts_on, special.name, special.id
            )
            FROM daily_specials special
            WHERE special.restaurant_id = restaurant.id
              AND special.status = 'active'
        ), '[]'::jsonb)
    )
    FROM restaurants restaurant
    WHERE restaurant.id = p_restaurant_id;
$$;

CREATE OR REPLACE FUNCTION refresh_public_menu_snapshot(p_restaurant_id uuid)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public, pg_catalog
AS $$
DECLARE
    payload jsonb;
BEGIN
    payload := build_public_menu_payload(p_restaurant_id);
    IF payload IS NULL THEN
        DELETE FROM public_menu_snapshots WHERE restaurant_id = p_restaurant_id;
        RETURN;
    END IF;

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

CREATE OR REPLACE FUNCTION refresh_public_menu_snapshot_from_child()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = public, pg_catalog
AS $$
DECLARE
    current_restaurant_id uuid;
    previous_restaurant_id uuid;
BEGIN
    IF TG_OP = 'DELETE' THEN
        current_restaurant_id := OLD.restaurant_id;
    ELSE
        current_restaurant_id := NEW.restaurant_id;
    END IF;

    IF TG_OP = 'UPDATE' THEN
        previous_restaurant_id := OLD.restaurant_id;
        IF previous_restaurant_id IS DISTINCT FROM current_restaurant_id THEN
            PERFORM refresh_public_menu_snapshot(previous_restaurant_id);
        END IF;
    END IF;

    PERFORM refresh_public_menu_snapshot(current_restaurant_id);
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

CREATE OR REPLACE FUNCTION refresh_public_menu_snapshot_from_restaurant()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = public, pg_catalog
AS $$
BEGIN
    PERFORM refresh_public_menu_snapshot(NEW.id);
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS restaurants_refresh_public_menu_snapshot ON restaurants;
CREATE TRIGGER restaurants_refresh_public_menu_snapshot
AFTER INSERT OR UPDATE OF name, slug, timezone, currency, status ON restaurants
FOR EACH ROW EXECUTE FUNCTION refresh_public_menu_snapshot_from_restaurant();

DROP TRIGGER IF EXISTS menus_refresh_public_menu_snapshot ON menus;
CREATE TRIGGER menus_refresh_public_menu_snapshot
AFTER INSERT OR UPDATE OR DELETE ON menus
FOR EACH ROW EXECUTE FUNCTION refresh_public_menu_snapshot_from_child();

DROP TRIGGER IF EXISTS catalog_items_refresh_public_menu_snapshot ON catalog_items;
CREATE TRIGGER catalog_items_refresh_public_menu_snapshot
AFTER INSERT OR UPDATE OR DELETE ON catalog_items
FOR EACH ROW EXECUTE FUNCTION refresh_public_menu_snapshot_from_child();

DROP TRIGGER IF EXISTS daily_specials_refresh_public_menu_snapshot ON daily_specials;
CREATE TRIGGER daily_specials_refresh_public_menu_snapshot
AFTER INSERT OR UPDATE OR DELETE ON daily_specials
FOR EACH ROW EXECUTE FUNCTION refresh_public_menu_snapshot_from_child();

DROP TRIGGER IF EXISTS business_hours_refresh_public_menu_snapshot ON business_hours;
CREATE TRIGGER business_hours_refresh_public_menu_snapshot
AFTER INSERT OR UPDATE OR DELETE ON business_hours
FOR EACH ROW EXECUTE FUNCTION refresh_public_menu_snapshot_from_child();

DROP TRIGGER IF EXISTS special_hours_refresh_public_menu_snapshot ON special_hours;
CREATE TRIGGER special_hours_refresh_public_menu_snapshot
AFTER INSERT OR UPDATE OR DELETE ON special_hours
FOR EACH ROW EXECUTE FUNCTION refresh_public_menu_snapshot_from_child();

CREATE OR REPLACE FUNCTION get_public_menu_snapshot(p_restaurant_slug text)
RETURNS TABLE (payload_jsonb jsonb, etag text, generated_at timestamptz)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_catalog
AS $$
    SELECT snapshot.payload_jsonb, snapshot.etag, snapshot.generated_at
    FROM public_menu_snapshots snapshot
    JOIN restaurants restaurant ON restaurant.id = snapshot.restaurant_id
    WHERE restaurant.slug = btrim(p_restaurant_slug)
      AND restaurant.status = 'active'
    LIMIT 1;
$$;

SELECT refresh_public_menu_snapshot(restaurant.id)
FROM restaurants restaurant;

INSERT INTO schema_migrations (version)
VALUES ('000001_restaurant_public_menu_snapshot')
ON CONFLICT (version) DO NOTHING;

COMMIT;
