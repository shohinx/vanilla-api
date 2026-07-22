BEGIN;

ALTER TABLE catalog_items
    ADD COLUMN IF NOT EXISTS price_cents integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS variants jsonb NOT NULL DEFAULT '[]'::jsonb;

-- Preserve any rows created by the short-lived normalized variant design,
-- then remove that design. Variants are an ordered value on a catalog item.
DO $$
BEGIN
    IF to_regclass('public.item_variants') IS NOT NULL THEN
        EXECUTE $migration$
            UPDATE catalog_items item
            SET variants = migrated.variants
            FROM (
                SELECT catalog_item_id,
                       jsonb_agg(
                           jsonb_build_object('name', name, 'price_cents', 0)
                           ORDER BY sort_order, name, id
                       ) AS variants
                FROM item_variants
                GROUP BY catalog_item_id
            ) migrated
            WHERE item.id = migrated.catalog_item_id
              AND item.variants = '[]'::jsonb
        $migration$;
    END IF;
END;
$$;

DROP TABLE IF EXISTS item_variants CASCADE;
DROP FUNCTION IF EXISTS touch_item_variant_updated_at();

-- Remove the older, ID-based variant schema as well. Historical order rows are
-- otherwise left intact; only their retired variant-option reference is removed.
ALTER TABLE IF EXISTS order_item
    DROP COLUMN IF EXISTS variant_option_id;
DROP TABLE IF EXISTS variant_option;
DROP TABLE IF EXISTS variant_group;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'catalog_items_price_nonnegative'
          AND conrelid = 'catalog_items'::regclass
    ) THEN
        ALTER TABLE catalog_items
            ADD CONSTRAINT catalog_items_price_nonnegative CHECK (price_cents >= 0);
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'catalog_items_variants_array'
          AND conrelid = 'catalog_items'::regclass
    ) THEN
        ALTER TABLE catalog_items
            ADD CONSTRAINT catalog_items_variants_array CHECK (jsonb_typeof(variants) = 'array');
    END IF;
END;
$$;

CREATE OR REPLACE FUNCTION refresh_public_menu_snapshot(p_restaurant_id uuid)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public, pg_catalog
AS $$
DECLARE
    payload jsonb;
    catalog_payload jsonb;
BEGIN
    payload := build_public_menu_payload(p_restaurant_id);
    IF payload IS NULL THEN
        DELETE FROM public_menu_snapshots WHERE restaurant_id = p_restaurant_id;
        RETURN;
    END IF;

    SELECT COALESCE(jsonb_agg(
        jsonb_build_object(
            'id', item.id,
            'name', item.name,
            'description', item.description,
            'price_cents', item.price_cents,
            'variants', item.variants
        ) ORDER BY item.name, item.id
    ), '[]'::jsonb)
    INTO catalog_payload
    FROM catalog_items item
    WHERE item.restaurant_id = p_restaurant_id
      AND item.status = 'active';

    payload := jsonb_set(payload, '{catalog_items}', catalog_payload, true);

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
VALUES ('000003_item_variants')
ON CONFLICT (version) DO NOTHING;

COMMIT;
