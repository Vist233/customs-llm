#!/bin/bash
# Reproduces opencode's failure reports against the fake upstream, with opencode in Docker going through the relay.
# Needs: customs up with target "fake" pointing at the fake upstream, and `customs fakeup` running.
# usage: repro.sh OUTDIR [TIMEOUT_SECONDS] [scenario ...]
out="$1"; limit="${2:-40}"; shift 2 2>/dev/null
mkdir -p "$out"
scenarios=("$@")
[ ${#scenarios[@]} -eq 0 ] && scenarios=(customs-ok fault-empty400 fault-empty500 fault-quota-text fault-html502 fault-reset fault-reset-midstream fault-no-terminal fault-stall-midstream fault-stall)
for m in "${scenarios[@]}"; do
  cfg='{"$schema":"https://opencode.ai/config.json","provider":{"fake":{"npm":"@ai-sdk/openai-compatible","name":"fake","options":{"baseURL":"http://host.docker.internal:9000/fake","apiKey":"customs-fake"},"models":{"'"$m"'":{"name":"'"$m"'"}}}}}'
  start=$(date +%s)
  docker run --rm -e OPENCODE_CONFIG_CONTENT="$cfg" --entrypoint timeout customs-opencode "$limit" opencode run -m "fake/$m" "say hi" >"$out/$m.txt" 2>&1
  code=$?
  echo "$m exit=$code elapsed=$(( $(date +%s) - start ))s"
done
