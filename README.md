# vanilla-api

Vanilla is the backend for a table-side bakery menu. One QR code is used on every table. The public menu only returns items that are currently available, drink customizations are validated and priced by the server, and payment remains in the bakery's existing POS. Submitted orders appear in a protected admin queue; inventory changes only after staff take POS payment and mark the order sold.

## Run locally

Requirements: Go 1.26+ and Docker.

```bash
cp .env.example .env
make docker-run
make run
```

The API starts on `http://localhost:8080`. It creates its PostgreSQL tables and starter menu automatically.

## QR destination

Create one Dub link and QR code for the bakery. Point the Dub link at the deployed API's `/menu` route, for example:

```text
https://api.example.com/menu
```

`/menu` sends a temporary redirect to `MENU_APP_URL`. This gives the printed QR code a stable destination while allowing the customer-facing web app URL to change later.

## API

### Get the live menu

```http
GET /api/v1/menu
```

The response is marked `Cache-Control: no-store` and omits items whose inventory is zero.

See [Postman examples](docs/postman-examples.md) for complete coffee, cake, pastry, and sweet-treat request bodies.

See [API workflow](docs/api-workflow.md) for the complete QR → customer menu → submitted order → admin queue → POS payment → sold inventory lifecycle.

Admins can create 1–100 menu items at once with `POST /api/v1/admin/menu/items`; see the [bulk Postman body](docs/postman-examples.md#14-create-one-or-many-menu-items).

Admin category dropdowns use `GET /api/v1/admin/menu/categories`, and custom category titles can be created with `POST /api/v1/admin/menu/categories`.

### Price an order plan

This validates current stock and each drink's required modifier groups. It does not reserve stock or place an order.

```http
POST /api/v1/order-plans/quote
Content-Type: application/json

{
  "items": [
    {
      "item_id": "latte",
      "quantity": 2,
      "option_ids": ["latte-12oz", "latte-oat"]
    }
  ]
}
```

### Sync an item's inventory

The bakery admin or a future POS webhook calls this whenever display-case inventory changes. Setting quantity to zero immediately removes the item from the public menu.

```http
PATCH /api/v1/admin/items/butter-croissant/inventory
Content-Type: application/json
X-Admin-Key: <ADMIN_API_KEY>

{
  "quantity": 0
}
```

## Commands

```bash
make build       # compile the API
make test        # run all tests
make docker-run  # start PostgreSQL
make docker-down # stop PostgreSQL
make watch       # run with Air live reload
```
