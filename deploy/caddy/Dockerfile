# Caddy with the rate_limit handler, which stock Caddy does not ship.
# Built on the container by deploy/update.sh, like the Go services.
#
# The plugin is pinned to a commit: its only release tag is years behind.
FROM caddy:2-builder AS build
RUN xcaddy build --with github.com/mholt/caddy-ratelimit@5625512f24f6f59d6f64fb3aafe5eecff0b286db

FROM caddy:2-alpine
COPY --from=build /usr/bin/caddy /usr/bin/caddy
