# syntax=docker/dockerfile:1
ARG NODE_IMAGE=node:22.20.0-bookworm-slim@sha256:b21fe589dfbe5cc39365d0544b9be3f1f33f55f3c86c87a76ff65a02f8f5848e
FROM ${NODE_IMAGE}
USER root
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl git python3 python3-yaml build-essential tini procps \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /opt/agentlab-tools
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --no-audit --no-fund \
    && for tool in codex claude opencode pi; do \
         ln -s "/opt/agentlab-tools/node_modules/.bin/$tool" "/usr/local/bin/$tool"; \
       done \
    && npm cache clean --force \
    && mkdir -p /workspace && chown node:node /workspace
ENV DISABLE_AUTOUPDATER=1 \
    CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 \
    OPENCODE_DISABLE_AUTOUPDATE=true \
    OPENCODE_DISABLE_MODELS_FETCH=true \
    OTEL_SDK_DISABLED=true
USER node
WORKDIR /workspace
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["sleep", "infinity"]
