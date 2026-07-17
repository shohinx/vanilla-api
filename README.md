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

### Upload a menu image

Menu images are stored in a private S3-compatible bucket (SeaweedFS in production) and streamed to customers through the API. Uploads require the admin key and accept JPEG, PNG, or WebP source files up to 50 MiB.

Before storage, the API validates the decoded image, rejects sources above 40 megapixels, applies JPEG EXIF orientation, scales the image to fit within 1600×1600, strips embedded metadata, and converts it to WebP at quality 80. Stored objects have a hard 2 MiB ceiling; unusually complex images are progressively reduced to 1200×1200 or 800×800 to meet it. Only the optimized WebP derivative is written to SeaweedFS; the original upload is not retained.

```bash
curl -X POST http://localhost:8080/api/v1/admin/images \
  -H "X-Admin-Key: $ADMIN_API_KEY" \
  -F "file=@carrot-cake.jpg"
```

The response contains an `image_url` that can be supplied when creating a menu item or attached later through the item image route. The field is optional when creating an item:

```json
{
  "key": "menu/9af70a4059f84be781a72850157eca43.webp",
  "image_url": "https://api.example.com/api/v1/images/menu/9af70a4059f84be781a72850157eca43.webp",
  "content_type": "image/webp",
  "size": 184203,
  "original_size": 8420112,
  "original_width": 4032,
  "original_height": 3024,
  "width": 1600,
  "height": 1200
}
```

The bucket stays private. `GET /api/v1/images/:key` reads the object using backend-only credentials and returns it with immutable cache headers.

To add, replace, or clear an existing item's image, upload the new image first and then patch the item with the returned `image_url`:

```http
PATCH /api/v1/admin/items/carrot-cake/image
Content-Type: application/json
X-Admin-Key: <ADMIN_API_KEY>

{
  "image_url": "https://api.example.com/api/v1/images/menu/9af70a4059f84be781a72850157eca43.webp"
}
```

Set `image_url` to an empty string to remove the image. Replacements use a newly uploaded URL so immutable client caches continue to work correctly.

Configure the deployment with:

```text
PUBLIC_BASE_URL=https://api.example.com
IMAGE_S3_ENDPOINT=https://seaweedfs.example.com
IMAGE_S3_REGION=us-east-1
IMAGE_S3_BUCKET=menu
IMAGE_S3_ACCESS_KEY=<bucket-scoped access key>
IMAGE_S3_SECRET_KEY=<bucket-scoped secret key>
```

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
