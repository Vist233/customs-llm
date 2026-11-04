#!/bin/bash
# Reproduces testset/results/litellm-*.json: starts LiteLLM in Docker and runs the test set with static models.
# usage: gateways/litellm/probe.sh [customs binary] [extra flags for customs probe run]
set -e
root="$(cd "$(dirname "$0")/../.." && pwd)"
bin="${1:-customs}"; shift || true
"$root/gateways/litellm/run.sh"
exec "$bin" probe run --name litellm --url http://127.0.0.1:4000 --key sk-customs \
  --alias "openai_responses->openai_chat=customs-chat-bridge" \
  --alias "anthropic_messages->openai_chat=customs-chat-bridge" \
  --alias "openai_chat->openai_responses=customs-responses-prefix" \
  --alias "route:mistral=customs-mistral@openai_chat" \
  --alias "*->gemini_generate=customs-gemini" --alias "route:gemini=customs-gemini@gemini_generate" \
  --alias "route:google=customs-gemini@gemini_generate" --alias "route:vertex=customs-gemini@gemini_generate" \
  --alias "*->bedrock_converse=customs-bedrock" --alias "route:bedrock=customs-bedrock@bedrock_converse" \
  --alias "*->ollama_chat=customs-ollama" --alias "route:ollama=customs-ollama@ollama_chat" \
  --alias "path:/canon-skills=passthrough" --satisfied litellm-49479-jsonrpc-id-stripped "$@"
