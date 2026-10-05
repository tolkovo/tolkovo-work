#!/usr/bin/env bash
# Forced command for the CI deploy key. SSH_ORIGINAL_COMMAND = image tag;
# stdin line 1 = short-lived registry token (optional).
set -euo pipefail
tag="${SSH_ORIGINAL_COMMAND:-}"
[[ "$tag" =~ ^[A-Za-z0-9._-]{1,128}$ ]] || { echo "bad tag"; exit 2; }
IFS= read -r token || token=""
REPO_IMAGE="${TOLKOVO_REGISTRY_IMAGE:-ghcr.io/tolkovo/tolkovo-work}"
IMAGE="$REPO_IMAGE:$tag"
cd /opt/tolkovo/src/deploy/production
exec 9>/opt/tolkovo/deploy.lock; flock -n 9 || { echo "another deploy is running"; exit 3; }

prev="$(grep '^TOLKOVO_IMAGE=' .env | cut -d= -f2-)"
echo "current: $prev -> new: $IMAGE"

if [ -n "$token" ]; then
  echo "$token" | docker login ghcr.io -u tolkovo-actions --password-stdin >/dev/null
  trap 'docker logout ghcr.io >/dev/null 2>&1 || true' EXIT
fi
docker image inspect "$IMAGE" >/dev/null 2>&1 || docker pull -q "$IMAGE" >/dev/null

mkdir -p /opt/tolkovo/backups
dump="/opt/tolkovo/backups/pre-$tag-$(date +%Y%m%d-%H%M%S).dump"
docker compose exec -T postgres pg_dump -U tolkovo -Fc tolkovo > "$dump"
echo "backup: $dump ($(du -h "$dump" | cut -f1))"
ls -1t /opt/tolkovo/backups/*.dump 2>/dev/null | tail -n +8 | xargs -r rm -f

setimg() { sed -i "s|^TOLKOVO_IMAGE=.*|TOLKOVO_IMAGE=$1|" .env; }
healthy() { for _ in $(seq 1 30); do
    curl -fsS -m 3 http://127.0.0.1:8080/readyz >/dev/null 2>&1 && return 0; sleep 3; done; return 1; }

setimg "$IMAGE"
if docker compose up -d --remove-orphans && healthy; then
  echo "deployed $IMAGE: $(curl -fsS -m 3 http://127.0.0.1:8080/healthz)"
else
  echo "NOT READY, rolling back to $prev"; docker compose logs --tail 40 app migrate || true
  setimg "$prev"; docker compose up -d --remove-orphans || true
  healthy && echo "rolled back to $prev (DB dump kept: $dump)" || echo "ROLLBACK ALSO NOT READY"
  exit 1
fi
docker image prune -f >/dev/null 2>&1 || true
