# opencode in a container, so nothing is installed on the host.
FROM node:22-slim
RUN apt-get update -qq && apt-get install -y -qq ca-certificates curl >/dev/null && npm install -g opencode-ai >/dev/null 2>&1
WORKDIR /work
ENTRYPOINT ["opencode"]
