#!/bin/bash
# Starts magpie in Docker for probing, isolated from any magpie on the host. Prints a gateway key.
set -e
docker rm -f customs-magpie >/dev/null 2>&1 || true
docker volume rm customs-magpie-config >/dev/null 2>&1 || true
docker run -d --name customs-magpie -p 127.0.0.1:3426:3425 -v customs-magpie-config:/config ghcr.io/yetone/magpie:latest >/dev/null
for _ in $(seq 1 60); do
  [ "$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:3426/v1/models)" != 000 ] && break
  sleep 1
done
docker exec customs-magpie magpie gateway-key add customs-probe
