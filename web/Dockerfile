# syntax=docker/dockerfile:1
# The web app (both consoles): built with Vite, served by nginx, which also
# proxies /api to the trade app and /sim to brokersim (same origin, no CORS),
# like the Vite dev server does under `make up`.

FROM node:24-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY . .
RUN npm run build

FROM nginx:1.30-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
HEALTHCHECK --interval=5s --timeout=3s --retries=6 CMD wget -qO- http://127.0.0.1/healthz >/dev/null || exit 1
