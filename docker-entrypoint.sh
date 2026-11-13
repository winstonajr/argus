#!/bin/sh
set -e

# Ensure /app/data directory exists and has write permissions
mkdir -p /app/data
chmod 777 /app/data 2>/dev/null || chown -R argus:argus /app/data 2>/dev/null || true

# Execute Argus dropping privileges to non-root argus user
exec su-exec argus /app/argus "$@"
