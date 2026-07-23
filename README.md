# vanilla-api

Vanilla is the backend for a customer-facing restaurant menu. One restaurant-wide Dub short link and QR code opens the complete menu. Customers can only view menu content; the API does not expose customer ordering, payment, inventory, or variant-selection workflows.

## Run locally

Requirements: Go 1.26+ and Docker.

```bash
cp .env.example .env
make docker-run
make run
```

The API starts on `http://localhost:8080`. It creates its PostgreSQL tables and starter menu automatically.

PostgreSQL can be configured with the `BLUEPRINT_DB_*` variables in `.env` or with a standard `DATABASE_URL`. When both are set, `DATABASE_URL` supplies the connection URI; `BLUEPRINT_DB_SSLMODE` and `BLUEPRINT_DB_SCHEMA` are added as defaults when the URI does not specify `sslmode` or `search_path` itself.

## QR menu link

Create and manage one restaurant-wide link in Dub, and point it at the customer-facing menu. Dub remains the source of truth for the short link, generated QR code, and analytics; the API does not store a local copy of the link.

Configure the backend with an API key created in the same Dub workspace as the existing link. Identify the link by its hostname and slug, without a URL scheme. For `https://dub.sh/diAI31C`, use:

```dotenv
DUB_DOMAIN=dub.sh
DUB_LINK_KEY=diAI31C
```

An authenticated restaurant administrator can retrieve the existing link and its Dub-generated QR image URL without modifying it:

```http
GET /api/v1/admin/organizations/:organizationID/restaurants/:restaurantID/menus/qr
```

## API

### Get the live menu

```http
GET /restaurants/:restaurantSlug/menu
```

This single response contains the restaurant's complete active customer-facing menu. It is served from the current generated snapshot with `ETag` and public cache headers; customers do not access snapshot IDs or snapshot-management routes.

### Menu schedules

Restaurant administrators manage a menu's availability windows under `GET|POST /menus/:menuID/schedules` and individual schedules under `/menu-schedules/:menuScheduleID`. `weekday_mask` uses Monday through Sunday as bits `1, 2, 4, 8, 16, 32, 64`; use `127` for every day. Empty date and local-time ranges mean the schedule is not bounded by that dimension.

Menu changes are applied directly. There is no draft, publishing, or historical-version layer.

### Menu sections and entries

Sections such as Coffee, Pastries, and Cakes belong directly to a menu under `/menus/:menuID/sections`. An entry places an existing catalog item in a section and stores only its display order and active state. Listing sections returns their ordered entries with complete catalog-item details, so the frontend does not need additional catalog-item requests.

Creating or updating an entry uses:

```json
{
  "catalog_item_id": "90000000-0000-4000-8000-000000000003",
  "sort_order": 10,
  "status": "active"
}
```

### Catalog item variants

Variants are optional display choices such as Small/Large or Slice/Whole Cake. They are an ordered `variants` array on the catalog item, not separate resources with their own IDs. Create or replace the complete array through `POST /catalog-items` or `PATCH /catalog-items/:catalogItemID`:

```json
{
  "name": "Cappuccino",
  "price_cents": 0,
  "variants": [
    { "name": "Small", "price_cents": 450 },
    { "name": "Medium", "price_cents": 525 },
    { "name": "Large", "price_cents": 600 }
  ],
  "status": "active"
}
```

Items without choices use the catalog item's `price_cents` and send `"variants": []`. The public menu returns each item with the same complete ordered array; customers only view it and do not select or submit a variant.

### Catalog item allergens

Allergen definitions belong to the organization and are managed under
`/api/v1/admin/organizations/:organizationID/allergens`. Each restaurant then
assigns the relevant organization allergens to its own catalog items, allowing
restaurants with different recipes to publish different disclosures.

Catalog item create and update requests replace the complete `allergens` array:

```json
{
  "name": "Almond Croissant",
  "price_cents": 650,
  "variants": [],
  "allergens": [
    {
      "allergen_id": "40000000-0000-4000-8000-000000000001",
      "relationship": "contains"
    },
    {
      "allergen_id": "40000000-0000-4000-8000-000000000002",
      "relationship": "may_contain"
    }
  ],
  "status": "active"
}
```

`relationship` must be `contains` or `may_contain`. The assigned allergen must
belong to the same organization as the catalog item's restaurant. Catalog item,
menu-entry, and public-menu responses return the resolved allergen name, code,
description, and relationship.

## Commands

```bash
make build       # compile the API
make test        # run all tests
make migrate     # apply PostgreSQL migrations
make docker-run  # start PostgreSQL
make docker-down # stop PostgreSQL
make watch       # run with Air live reload
```
