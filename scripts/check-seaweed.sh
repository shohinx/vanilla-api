#!/bin/sh

set -eu

env_file=${ENV_FILE:-.env}
if [ ! -f "$env_file" ]; then
	echo "$env_file does not exist; run make seaweed-provision first" >&2
	exit 1
fi

set -a
# shellcheck disable=SC1090
. "$env_file"
set +a

for name in IMAGE_S3_ENDPOINT IMAGE_S3_REGION IMAGE_S3_BUCKET IMAGE_S3_ACCESS_KEY IMAGE_S3_SECRET_KEY; do
	value=$(eval "printf '%s' \"\${$name:-}\"")
	if [ -z "$value" ]; then
		echo "$name is missing from $env_file" >&2
		exit 1
	fi
done

if ! command -v aws >/dev/null 2>&1; then
	echo "Required command not found: aws" >&2
	exit 1
fi

AWS_ACCESS_KEY_ID="$IMAGE_S3_ACCESS_KEY" \
AWS_SECRET_ACCESS_KEY="$IMAGE_S3_SECRET_KEY" \
AWS_DEFAULT_REGION="$IMAGE_S3_REGION" \
	aws --endpoint-url "$IMAGE_S3_ENDPOINT" s3api head-bucket \
		--bucket "$IMAGE_S3_BUCKET"

echo "SeaweedFS access check passed for existing bucket: s3://$IMAGE_S3_BUCKET"
