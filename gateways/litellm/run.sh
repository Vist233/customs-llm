#!/bin/bash
# Starts LiteLLM + Postgres in Docker with the probe config. Nothing is installed on the host.
set -e
cd "$(dirname "$0")"
docker compose up -d --quiet-pull >/dev/null 2>&1
for _ in $(seq 1 90); do
  [ "$(curl -s -o /dev/null -w '%{http_code}' -H 'Authorization: Bearer sk-customs' http://127.0.0.1:4000/v1/models)" = 200 ] && break
  sleep 2
done
echo "litellm ready on http://127.0.0.1:4000 (key sk-customs)"
