#!/usr/bin/env bash
# Configures the dev Garage once after `make deps-up`: assigns the single node
# a layout, creates the bucket and imports the fixed dev access key from
# .env.example. Safe to re-run: it stops when the bucket already exists.
# The garage image has no shell, so every command runs the binary via exec.
set -euo pipefail

cd "$(dirname "$0")"
set -a
# shellcheck source=/dev/null
. ./.env.example
set +a

garage() {
	docker compose -f docker-compose.dev.yml --env-file .env.example exec -T garage /garage "$@"
}

if garage bucket info "$GARAGE_BUCKET" >/dev/null 2>&1; then
	echo "garage: bucket $GARAGE_BUCKET is ready"
	exit 0
fi

node_id="$(garage node id -q)"
node_id="${node_id%%@*}"
garage layout assign -z dev -c 1G "$node_id"
garage layout apply --version 1
garage bucket create "$GARAGE_BUCKET"
garage key import --yes -n tolkovo-dev "$GARAGE_KEY_ID" "$GARAGE_SECRET_KEY"
garage bucket allow --read --write --owner "$GARAGE_BUCKET" --key "$GARAGE_KEY_ID"
echo "garage: bucket $GARAGE_BUCKET created, S3 endpoint http://localhost:3900 (region garage)"
