#!/bin/sh
set -e

mkdir -p /app/data
chown -R argus:argus /app/data 2>/dev/null || true

exec su-exec argus /app/argus "$@"
